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
	"regexp"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/application"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Processor interface {
	ProcessEvent(context.Context, domain.EventMeta, json.RawMessage) error
}

type deadLetterStore interface {
	RecordDeadLetter(context.Context, string, int32, int64, string, string) error
}

type consumerClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	AllowRebalance()
	CommitRecords(context.Context, ...*kgo.Record) error
	SetOffsets(map[string]map[int32]kgo.EpochOffset)
	PauseFetchPartitions(map[string][]int32) map[string][]int32
	ResumeFetchPartitions(map[string][]int32)
}

type topicPartition struct {
	topic     string
	partition int32
}

type Consumer struct {
	client     consumerClient
	processor  Processor
	store      deadLetterStore
	logger     *zap.Logger
	retryDelay time.Duration
	deferred   map[topicPartition]*kgo.Record
	metrics    platformkafka.Observer
}

func NewConsumer(client consumerClient, processor Processor, store deadLetterStore, logger *zap.Logger, retryDelay time.Duration, observers ...platformkafka.Observer) *Consumer {
	return &Consumer{client: client, processor: processor, store: store, logger: logger, retryDelay: retryDelay, deferred: make(map[topicPartition]*kgo.Record), metrics: platformkafka.ObserverOrNoop(observers...)}
}

func (c *Consumer) Run(ctx context.Context) {
	for c.pollOnce(ctx) {
	}
}

func (c *Consumer) pollOnce(ctx context.Context) bool {
	pollCtx := ctx
	cancel := func() {}
	if len(c.deferred) > 0 {
		pollCtx, cancel = context.WithTimeout(ctx, c.retryDelay)
	}
	fetches := c.client.PollRecords(pollCtx, 1)
	cancel()
	defer c.client.AllowRebalance()
	for _, fetchErr := range fetches.Errors() {
		if ctx.Err() == nil {
			c.logger.Warn("provisioning Kafka partition fetch failed", zap.String("error_type", fmt.Sprintf("%T", fetchErr.Err)), zap.String("topic", fetchErr.Topic), zap.Int32("partition", fetchErr.Partition))
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
		err := c.processTraced(ctx, record)
		if errors.Is(err, domain.ErrSequenceGap) {
			c.metrics.ObserveHandler(record.Topic, "deferred", startedAt, record.Timestamp)
			c.metrics.ObserveRetry(record.Topic, "consumer")
			c.deferGap(record)
			continue
		}
		if err != nil {
			code, poison := application.ContractErrorCode(err)
			if !poison {
				c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.rewind(records[index:])
				c.waitRetry(ctx, err)
				return ctx.Err() == nil
			}
			sum := sha256.Sum256(record.Value)
			if err := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); err != nil {
				c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.rewind(records[index:])
				c.waitRetry(ctx, err)
				return ctx.Err() == nil
			}
			c.metrics.ObserveDLQ(record.Topic)
			outcome = "dead_letter"
		}
		if !c.commit(ctx, record) {
			c.metrics.ObserveHandler(record.Topic, "commit_error", startedAt, record.Timestamp)
			c.metrics.ObserveRetry(record.Topic, "commit")
			c.rewind(records[index:])
			return ctx.Err() == nil
		}
		c.metrics.ObserveHandler(record.Topic, outcome, startedAt, record.Timestamp)
		c.retryDeferred(ctx)
	}
	if len(records) == 0 && len(c.deferred) > 0 {
		c.retryDeferred(ctx)
	}
	return ctx.Err() == nil
}

func (c *Consumer) process(ctx context.Context, record *kgo.Record) error {
	var envelope platformkafka.Envelope
	if err := strictDecode(record.Value, &envelope); err != nil {
		return &application.ContractError{Code: "invalid_envelope_json"}
	}
	if err := validateEnvelope(record, envelope); err != nil {
		return &application.ContractError{Code: err.Error()}
	}
	sum := sha256.Sum256(record.Value)
	meta := domain.EventMeta{
		EventID: envelope.EventID, EventType: envelope.EventType, AggregateID: envelope.AggregateID,
		AggregateSequence: envelope.AggregateSequence, PartitionKey: envelope.PartitionKey,
		CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, OccurredAt: envelope.OccurredAt.UTC(),
		SourceTopic: record.Topic, SourcePartition: record.Partition, SourceOffset: record.Offset, PayloadSHA256: hex.EncodeToString(sum[:]),
	}
	return c.processor.ProcessEvent(ctx, meta, envelope.Data)
}

func validateEnvelope(record *kgo.Record, envelope platformkafka.Envelope) error {
	if record.Topic != "access.provision.request.v1" && record.Topic != "access.revoke.request.v1" {
		return errors.New("invalid_envelope_routing")
	}
	if envelope.EventType != record.Topic || envelope.SchemaVersion != 1 || envelope.Producer != "access-service" || envelope.AggregateType != "credential" {
		return errors.New("invalid_envelope_routing")
	}
	if !uuidPattern.MatchString(envelope.EventID) || !uuidPattern.MatchString(envelope.AggregateID) || !uuidPattern.MatchString(envelope.CorrelationID) || envelope.OccurredAt.IsZero() || envelope.AggregateSequence < 1 {
		return errors.New("invalid_envelope_identifiers")
	}
	if envelope.CausationID != nil && !uuidPattern.MatchString(*envelope.CausationID) {
		return errors.New("invalid_envelope_causation")
	}
	if envelope.PartitionKey != "credential:"+envelope.AggregateID || string(record.Key) != envelope.PartitionKey {
		return errors.New("invalid_envelope_partition_key")
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return errors.New("invalid_envelope_data")
	}
	return nil
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func (c *Consumer) deferGap(record *kgo.Record) {
	key := topicPartition{topic: record.Topic, partition: record.Partition}
	if _, exists := c.deferred[key]; exists {
		return
	}
	c.deferred[key] = record
	c.client.PauseFetchPartitions(map[string][]int32{record.Topic: {record.Partition}})
	c.logger.Warn("provisioning command sequence gap deferred", zap.String("topic", record.Topic), zap.Int32("partition", record.Partition), zap.Int64("offset", record.Offset))
}

func (c *Consumer) retryDeferred(ctx context.Context) {
	for {
		progressed := false
		for key, record := range c.deferred {
			startedAt := time.Now()
			outcome := "success"
			err := c.processTraced(ctx, record)
			if errors.Is(err, domain.ErrSequenceGap) {
				c.metrics.ObserveHandler(record.Topic, "deferred", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				continue
			}
			if err != nil {
				code, poison := application.ContractErrorCode(err)
				if !poison {
					c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
					c.metrics.ObserveRetry(record.Topic, "consumer")
					continue
				}
				sum := sha256.Sum256(record.Value)
				if err := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); err != nil {
					c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
					c.metrics.ObserveRetry(record.Topic, "consumer")
					continue
				}
				c.metrics.ObserveDLQ(record.Topic)
				outcome = "dead_letter"
			}
			if !c.commit(ctx, record) {
				c.metrics.ObserveHandler(record.Topic, "commit_error", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "commit")
				continue
			}
			c.metrics.ObserveHandler(record.Topic, outcome, startedAt, record.Timestamp)
			delete(c.deferred, key)
			c.client.ResumeFetchPartitions(map[string][]int32{record.Topic: {record.Partition}})
			progressed = true
		}
		if !progressed {
			return
		}
	}
}

func (c *Consumer) processTraced(ctx context.Context, record *kgo.Record) error {
	processCtx, finish := platformtelemetry.StartKafkaConsumer(ctx, record)
	err := c.process(processCtx, record)
	finish(err)
	return err
}

func (c *Consumer) commit(ctx context.Context, record *kgo.Record) bool {
	commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.client.CommitRecords(commitCtx, record); err != nil {
		c.waitRetry(ctx, err)
		return false
	}
	return true
}

func (c *Consumer) rewind(records []*kgo.Record) {
	offsets := make(map[string]map[int32]kgo.EpochOffset)
	for _, record := range records {
		if offsets[record.Topic] == nil {
			offsets[record.Topic] = make(map[int32]kgo.EpochOffset)
		}
		if _, exists := offsets[record.Topic][record.Partition]; !exists {
			offsets[record.Topic][record.Partition] = kgo.EpochOffset{Offset: record.Offset, Epoch: -1}
		}
	}
	c.client.SetOffsets(offsets)
}

func (c *Consumer) waitRetry(ctx context.Context, err error) {
	c.logger.Warn("provisioning Kafka record will retry", zap.String("error_type", fmt.Sprintf("%T", err)))
	timer := time.NewTimer(c.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
