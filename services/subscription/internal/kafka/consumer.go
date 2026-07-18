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
	client     *kgo.Client
	service    *application.Service
	store      domain.Store
	logger     *zap.Logger
	retryDelay time.Duration
}

func NewConsumer(client *kgo.Client, service *application.Service, store domain.Store, logger *zap.Logger, retryDelay time.Duration) *Consumer {
	return &Consumer{client: client, service: service, store: store, logger: logger, retryDelay: retryDelay}
}

func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, 1)
		if err := fetches.Err(); err != nil {
			if ctx.Err() == nil {
				c.logger.Warn("subscription Kafka poll failed", zap.String("error_type", fmt.Sprintf("%T", err)))
			}
			continue
		}
		records := fetches.Records()
		if len(records) == 0 {
			c.client.AllowRebalance()
			continue
		}
		record := records[0]
		err := c.process(ctx, record)
		if err != nil {
			if code, poison := application.ContractErrorCode(err); poison {
				sum := sha256.Sum256(record.Value)
				if deadErr := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); deadErr != nil {
					c.rewind(record)
					c.logRetry(ctx, deadErr)
					c.client.AllowRebalance()
					continue
				}
			} else {
				c.rewind(record)
				c.logRetry(ctx, err)
				c.client.AllowRebalance()
				continue
			}
		}
		commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.client.CommitRecords(commitCtx, record)
		cancel()
		if err != nil {
			c.rewind(record)
			c.logRetry(ctx, err)
		}
		c.client.AllowRebalance()
	}
}

func (c *Consumer) process(ctx context.Context, record *kgo.Record) error {
	var envelope platformkafka.Envelope
	if err := strictDecode(record.Value, &envelope); err != nil {
		return &application.ContractError{Code: "invalid_envelope"}
	}
	if envelope.SchemaVersion != 1 || envelope.EventType != record.Topic || envelope.Producer != "billing-service" || envelope.PartitionKey != string(record.Key) {
		return &application.ContractError{Code: "invalid_envelope_metadata"}
	}
	meta := domain.EventMeta{EventID: envelope.EventID, EventType: envelope.EventType, AggregateID: envelope.AggregateID, CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, OccurredAt: envelope.OccurredAt}
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

func (c *Consumer) rewind(record *kgo.Record) {
	c.client.SetOffsets(map[string]map[int32]kgo.EpochOffset{
		record.Topic: {record.Partition: {Epoch: record.LeaderEpoch, Offset: record.Offset}},
	})
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
