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
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/services/notification/internal/application"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Processor interface {
	ProcessEvent(context.Context, domain.EventMeta, json.RawMessage) error
}

type Store interface {
	RecordDeadLetter(context.Context, string, int32, int64, string, string, string) error
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
	store      Store
	logger     *zap.Logger
	retryDelay time.Duration
	deferred   map[topicPartition]*kgo.Record
	metrics    platformkafka.Observer
}

func NewConsumer(client consumerClient, processor Processor, store Store, logger *zap.Logger, retryDelay time.Duration, observers ...platformkafka.Observer) *Consumer {
	return &Consumer{client: client, processor: processor, store: store, logger: logger, retryDelay: retryDelay, deferred: make(map[topicPartition]*kgo.Record), metrics: platformkafka.ObserverOrNoop(observers...)}
}

func (c *Consumer) Run(ctx context.Context) {
	for c.pollOnce(ctx) {
	}
}

func (c *Consumer) pollOnce(ctx context.Context) bool {
	pollCtx, cancel := context.WithCancel(ctx)
	if len(c.deferred) > 0 {
		pollCtx, cancel = context.WithTimeout(ctx, c.retryDelay)
	}
	defer cancel()
	fetches := c.client.PollRecords(pollCtx, 1)
	defer c.client.AllowRebalance()
	for _, fetchErr := range fetches.Errors() {
		if ctx.Err() == nil {
			c.logger.Warn("notification Kafka fetch failed", zap.String("error_type", fmt.Sprintf("%T", fetchErr.Err)), zap.String("topic", fetchErr.Topic), zap.Int32("partition", fetchErr.Partition))
		}
	}
	for index, record := range fetches.Records() {
		if ctx.Err() != nil {
			c.rewind(fetches.Records()[index:])
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
				c.rewind(fetches.Records()[index:])
				c.wait(ctx)
				return ctx.Err() == nil
			}
			if err := c.deadLetter(ctx, record, code); err != nil {
				c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.rewind(fetches.Records()[index:])
				c.wait(ctx)
				return ctx.Err() == nil
			}
			c.metrics.ObserveDLQ(record.Topic)
			outcome = "dead_letter"
		}
		if !c.commit(ctx, record) {
			c.metrics.ObserveHandler(record.Topic, "commit_error", startedAt, record.Timestamp)
			c.metrics.ObserveRetry(record.Topic, "commit")
			c.rewind(fetches.Records()[index:])
			c.wait(ctx)
			return ctx.Err() == nil
		}
		c.metrics.ObserveHandler(record.Topic, outcome, startedAt, record.Timestamp)
		c.retryDeferred(ctx)
	}
	if len(fetches.Records()) == 0 {
		c.retryDeferred(ctx)
	}
	return ctx.Err() == nil
}

func (c *Consumer) process(ctx context.Context, record *kgo.Record) error {
	var envelope platformkafka.Envelope
	if err := strictDecode(record.Value, &envelope); err != nil {
		return &application.ContractError{Code: "invalid_envelope_json", Err: err}
	}
	if err := validateEnvelope(record, envelope); err != nil {
		return &application.ContractError{Code: err.Error(), Err: err}
	}
	sum := sha256.Sum256(record.Value)
	meta := domain.EventMeta{
		EventID: envelope.EventID, EventType: envelope.EventType, Producer: envelope.Producer,
		AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID, AggregateSequence: envelope.AggregateSequence,
		PartitionKey: envelope.PartitionKey, CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID,
		OccurredAt: envelope.OccurredAt.UTC(), SourceTopic: record.Topic, SourcePartition: record.Partition,
		SourceOffset: record.Offset, PayloadSHA256: hex.EncodeToString(sum[:]),
	}
	return c.processor.ProcessEvent(ctx, meta, envelope.Data)
}

type envelopeRule struct {
	producer      string
	aggregateType string
	sequenced     bool
}

var rules = map[string]envelopeRule{
	"billing.payment.succeeded.v1":  {"billing-service", "payment", false},
	"billing.refund.succeeded.v1":   {"billing-service", "refund", false},
	"subscription.activated.v1":     {"subscription-service", "subscription", true},
	"subscription.extended.v1":      {"subscription-service", "subscription", true},
	"subscription.grace.started.v1": {"subscription-service", "subscription", true},
	"subscription.expired.v1":       {"subscription-service", "subscription", true},
	"subscription.revoked.v1":       {"subscription-service", "subscription", true},
	"access.ready.v1":               {"access-service", "access", true},
	"access.provisioning.failed.v1": {"access-service", "access", true},
	"access.revoked.v1":             {"access-service", "access", true},
}

func validateEnvelope(record *kgo.Record, envelope platformkafka.Envelope) error {
	rule, ok := rules[record.Topic]
	if !ok || envelope.EventType != record.Topic || envelope.SchemaVersion != 1 || envelope.Producer != rule.producer || envelope.AggregateType != rule.aggregateType {
		return errors.New("invalid_envelope_routing")
	}
	if !uuidPattern.MatchString(envelope.EventID) || !uuidPattern.MatchString(envelope.AggregateID) || !uuidPattern.MatchString(envelope.CorrelationID) || envelope.OccurredAt.IsZero() {
		return errors.New("invalid_envelope_identifiers")
	}
	if envelope.CausationID != nil && !uuidPattern.MatchString(*envelope.CausationID) {
		return errors.New("invalid_envelope_causation")
	}
	if !strings.HasPrefix(envelope.PartitionKey, "user:") || !uuidPattern.MatchString(strings.TrimPrefix(envelope.PartitionKey, "user:")) || string(record.Key) != envelope.PartitionKey {
		return errors.New("invalid_envelope_partition_key")
	}
	if rule.sequenced && envelope.AggregateSequence < 1 || !rule.sequenced && envelope.AggregateSequence != 0 {
		return errors.New("invalid_envelope_sequence")
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

func (c *Consumer) deadLetter(ctx context.Context, record *kgo.Record, reason string) error {
	sum := sha256.Sum256(record.Value)
	eventType := ""
	var envelope struct {
		EventType string `json:"event_type"`
	}
	if json.Unmarshal(record.Value, &envelope) == nil {
		if _, ok := rules[envelope.EventType]; ok {
			eventType = envelope.EventType
		}
	}
	return c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), eventType, reason)
}

func (c *Consumer) commit(ctx context.Context, record *kgo.Record) bool {
	commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.client.CommitRecords(commitCtx, record) == nil
}

func (c *Consumer) deferGap(record *kgo.Record) {
	key := topicPartition{record.Topic, record.Partition}
	if _, exists := c.deferred[key]; exists {
		return
	}
	c.deferred[key] = record
	c.client.PauseFetchPartitions(map[string][]int32{record.Topic: {record.Partition}})
	c.logger.Warn("notification ordered event gap deferred", zap.String("topic", record.Topic), zap.Int32("partition", record.Partition), zap.Int64("offset", record.Offset))
}

func (c *Consumer) retryDeferred(ctx context.Context) {
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
			if c.deadLetter(ctx, record, code) != nil {
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
	}
}

func (c *Consumer) processTraced(ctx context.Context, record *kgo.Record) error {
	processCtx, finish := platformtelemetry.StartKafkaConsumer(ctx, record)
	err := c.process(processCtx, record)
	finish(err)
	return err
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

func (c *Consumer) wait(ctx context.Context) {
	timer := time.NewTimer(c.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
