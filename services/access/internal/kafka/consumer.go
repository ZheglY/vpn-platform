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
	"github.com/ZheglY/vpn-platform/services/access/internal/application"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
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
	cancelPoll := func() {}
	if len(c.deferred) > 0 {
		pollCtx, cancelPoll = context.WithTimeout(ctx, c.retryDelay)
	}
	fetches := c.client.PollRecords(pollCtx, 1)
	cancelPoll()
	defer c.client.AllowRebalance()
	for _, fetchErr := range fetches.Errors() {
		if ctx.Err() == nil {
			c.logger.Warn("access Kafka partition fetch failed", zap.String("error_type", fmt.Sprintf("%T", fetchErr.Err)), zap.String("topic", fetchErr.Topic), zap.Int32("partition", fetchErr.Partition))
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
			if isSequenceGap(err) {
				c.metrics.ObserveHandler(record.Topic, "deferred", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.deferGap(record)
				continue
			}
			if code, poison := application.ContractErrorCode(err); poison {
				sum := sha256.Sum256(record.Value)
				if deadErr := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); deadErr != nil {
					c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
					c.metrics.ObserveRetry(record.Topic, "consumer")
					c.rewind(records[index:])
					c.waitRetry(ctx, deadErr)
					return ctx.Err() == nil
				}
				c.metrics.ObserveDLQ(record.Topic)
				outcome = "dead_letter"
			} else {
				c.metrics.ObserveHandler(record.Topic, "retry", startedAt, record.Timestamp)
				c.metrics.ObserveRetry(record.Topic, "consumer")
				c.rewind(records[index:])
				c.waitRetry(ctx, err)
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
			c.waitRetry(ctx, err)
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
		return application.ContractError("invalid_envelope_json", err)
	}
	if err := validateEnvelope(record, envelope); err != nil {
		return application.ContractError(err.Error(), err)
	}
	sum := sha256.Sum256(record.Value)
	meta := domain.EventMeta{
		EventID: envelope.EventID, EventType: envelope.EventType, AggregateID: envelope.AggregateID,
		AggregateSequence: envelope.AggregateSequence, PartitionKey: envelope.PartitionKey,
		CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, OccurredAt: envelope.OccurredAt.UTC(),
		SourceTopic: record.Topic, SourcePartition: record.Partition, SourceOffset: record.Offset,
		PayloadSHA256: hex.EncodeToString(sum[:]),
	}
	return c.processor.ProcessEvent(ctx, meta, envelope.Data)
}

type envelopeRule struct {
	producer        string
	aggregateType   string
	partitionPrefix string
}

var envelopeRules = map[string]envelopeRule{
	"subscription.activated.v1":     {"subscription-service", "subscription", "user:"},
	"subscription.extended.v1":      {"subscription-service", "subscription", "user:"},
	"subscription.grace.started.v1": {"subscription-service", "subscription", "user:"},
	"subscription.expired.v1":       {"subscription-service", "subscription", "user:"},
	"subscription.revoked.v1":       {"subscription-service", "subscription", "user:"},
	"access.provision.succeeded.v1": {"provisioning-service", "credential", "credential:"},
	"access.provision.failed.v1":    {"provisioning-service", "credential", "credential:"},
	"access.revoke.succeeded.v1":    {"provisioning-service", "credential", "credential:"},
	"access.revoke.failed.v1":       {"provisioning-service", "credential", "credential:"},
}

func validateEnvelope(record *kgo.Record, envelope platformkafka.Envelope) error {
	rule, ok := envelopeRules[record.Topic]
	if !ok || envelope.EventType != record.Topic || envelope.SchemaVersion != 1 || envelope.Producer != rule.producer || envelope.AggregateType != rule.aggregateType {
		return errors.New("invalid_envelope_routing")
	}
	if !uuidPattern.MatchString(envelope.EventID) || !uuidPattern.MatchString(envelope.AggregateID) || !uuidPattern.MatchString(envelope.CorrelationID) || envelope.OccurredAt.IsZero() {
		return errors.New("invalid_envelope_identifiers")
	}
	if envelope.CausationID != nil && !uuidPattern.MatchString(*envelope.CausationID) {
		return errors.New("invalid_envelope_causation")
	}
	if envelope.PartitionKey != rule.partitionPrefix+partitionID(envelope.PartitionKey) || !uuidPattern.MatchString(partitionID(envelope.PartitionKey)) || string(record.Key) != envelope.PartitionKey {
		return errors.New("invalid_envelope_partition_key")
	}
	if rule.partitionPrefix == "credential:" && partitionID(envelope.PartitionKey) != envelope.AggregateID {
		return errors.New("invalid_envelope_aggregate_key")
	}
	if envelope.AggregateSequence < 1 {
		return errors.New("invalid_envelope_sequence")
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return errors.New("invalid_envelope_data")
	}
	return nil
}

func partitionID(key string) string {
	if _, value, ok := strings.Cut(key, ":"); ok {
		return value
	}
	return ""
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

func (c *Consumer) rewind(records []*kgo.Record) {
	offsets := make(map[string]map[int32]kgo.EpochOffset)
	for _, record := range records {
		partitions := offsets[record.Topic]
		if partitions == nil {
			partitions = make(map[int32]kgo.EpochOffset)
			offsets[record.Topic] = partitions
		}
		if _, exists := partitions[record.Partition]; !exists {
			partitions[record.Partition] = kgo.EpochOffset{Offset: record.Offset, Epoch: -1}
		}
	}
	c.client.SetOffsets(offsets)
}

func (c *Consumer) deferGap(record *kgo.Record) {
	key := topicPartition{topic: record.Topic, partition: record.Partition}
	if _, exists := c.deferred[key]; exists {
		return
	}
	c.deferred[key] = record
	c.client.PauseFetchPartitions(map[string][]int32{record.Topic: {record.Partition}})
	c.logger.Warn("access ordered event gap deferred", zap.String("topic", record.Topic), zap.Int32("partition", record.Partition), zap.Int64("offset", record.Offset))
}

func (c *Consumer) retryDeferred(ctx context.Context) {
	for {
		progressed := false
		for key, record := range c.deferred {
			if ctx.Err() != nil {
				return
			}
			startedAt := time.Now()
			outcome := "success"
			err := c.process(ctx, record)
			if isSequenceGap(err) {
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
			commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = c.client.CommitRecords(commitCtx, record)
			cancel()
			if err != nil {
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

func isSequenceGap(err error) bool {
	return errors.Is(err, domain.ErrLifecycleSequenceGap) || errors.Is(err, domain.ErrOutcomeSequenceGap)
}

func (c *Consumer) waitRetry(ctx context.Context, err error) {
	c.logger.Warn("access Kafka record will retry", zap.String("error_type", fmt.Sprintf("%T", err)))
	timer := time.NewTimer(c.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
