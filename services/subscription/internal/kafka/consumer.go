package kafka

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/application"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

type Consumer struct {
	client     consumerClient
	service    *application.Service
	store      domain.Store
	logger     *zap.Logger
	retryDelay time.Duration
	metrics    platformkafka.Observer
}

type consumerClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	AllowRebalance()
	CommitRecords(context.Context, ...*kgo.Record) error
	SetOffsets(map[string]map[int32]kgo.EpochOffset)
}

func NewConsumer(client consumerClient, service *application.Service, store domain.Store, logger *zap.Logger, retryDelay time.Duration, observers ...platformkafka.Observer) *Consumer {
	return &Consumer{client: client, service: service, store: store, logger: logger, retryDelay: retryDelay, metrics: platformkafka.ObserverOrNoop(observers...)}
}

func (c *Consumer) Run(ctx context.Context) {
	for c.pollOnce(ctx) {
	}
}

func (c *Consumer) pollOnce(ctx context.Context) (keepRunning bool) {
	fetches := c.client.PollRecords(ctx, 1)
	defer c.client.AllowRebalance()

	for _, fetchErr := range fetches.Errors() {
		if ctx.Err() == nil {
			c.logger.Warn("subscription Kafka partition fetch failed",
				zap.String("error_type", fmt.Sprintf("%T", fetchErr.Err)),
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
			)
		}
	}

	records := fetches.Records()
	for index, record := range records {
		if ctx.Err() != nil {
			c.rewind(records[index:])
			return false
		}
		startedAt := time.Now()
		outcome := "success"
		err := c.process(ctx, record)
		if err != nil {
			if code, poison := application.ContractErrorCode(err); poison {
				sum := sha256.Sum256(record.Value)
				if deadErr := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); deadErr != nil {
					c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
					c.metrics.ObserveRetry(record.Topic, "consumer")
					c.rewind(records[index:])
					c.logRetry(ctx, deadErr)
					return ctx.Err() == nil
				}
				c.metrics.ObserveDLQ(record.Topic)
				outcome = "dead_letter"
			} else {
				c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.rewind(records[index:])
				c.logRetry(ctx, err)
				return ctx.Err() == nil
			}
		}
		commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.client.CommitRecords(commitCtx, record)
		cancel()
		if err != nil {
			c.metrics.ObserveHandler(record.Topic, "commit_error", startedAt, record.Timestamp)
			c.metrics.ObserveRetry(record.Topic, "commit")
			c.rewind(records[index:])
			c.logRetry(ctx, err)
			return ctx.Err() == nil
		}
		c.metrics.ObserveHandler(record.Topic, outcome, startedAt, record.Timestamp)
	}
	return ctx.Err() == nil
}

func (c *Consumer) process(ctx context.Context, record *kgo.Record) error {
	var envelope platformkafka.Envelope
	if err := strictDecode(record.Value, &envelope); err != nil {
		return &application.ContractError{Code: "invalid_envelope"}
	}
	if envelope.SchemaVersion != 1 || envelope.EventType != record.Topic || envelope.Producer != "billing-service" || envelope.PartitionKey != string(record.Key) {
		return &application.ContractError{Code: "invalid_envelope_metadata"}
	}
	sum := sha256.Sum256(record.Value)
	meta := domain.EventMeta{
		EventID: envelope.EventID, EventType: envelope.EventType, AggregateID: envelope.AggregateID,
		CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, OccurredAt: envelope.OccurredAt,
		SourceTopic: record.Topic, SourcePartition: record.Partition, SourceOffset: record.Offset,
		PayloadSHA256: hex.EncodeToString(sum[:]),
	}
	switch record.Topic {
	case "billing.payment.succeeded.v1":
		if envelope.AggregateType != "payment" {
			return &application.ContractError{Code: "invalid_payment_aggregate"}
		}
		var payment domain.PaymentSucceeded
		if err := strictDecode(envelope.Data, &payment); err != nil || envelope.PartitionKey != "user:"+payment.UserID {
			return &application.ContractError{Code: "invalid_payment_payload"}
		}
		return c.service.HandlePayment(ctx, meta, payment)
	case "billing.refund.succeeded.v1":
		if envelope.AggregateType != "refund" {
			return &application.ContractError{Code: "invalid_refund_aggregate"}
		}
		var refund domain.RefundSucceeded
		if err := strictDecode(envelope.Data, &refund); err != nil || envelope.PartitionKey != "user:"+refund.UserID {
			return &application.ContractError{Code: "invalid_refund_payload"}
		}
		return c.service.HandleRefund(ctx, meta, refund)
	default:
		return &application.ContractError{Code: "unsupported_event_type"}
	}
}

func strictDecode(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func (c *Consumer) rewind(records []*kgo.Record) {
	offsets := make(map[string]map[int32]kgo.EpochOffset)
	for _, record := range records {
		partitions := offsets[record.Topic]
		if partitions == nil {
			partitions = make(map[int32]kgo.EpochOffset)
			offsets[record.Topic] = partitions
		}
		if current, exists := partitions[record.Partition]; !exists || record.Offset < current.Offset {
			partitions[record.Partition] = kgo.EpochOffset{Epoch: record.LeaderEpoch, Offset: record.Offset}
		}
	}
	if len(offsets) > 0 {
		c.client.SetOffsets(offsets)
	}
}

func (c *Consumer) logRetry(ctx context.Context, err error) {
	c.logger.Warn("subscription Kafka record will retry", zap.String("error_type", fmt.Sprintf("%T", err)))
	timer := time.NewTimer(c.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
