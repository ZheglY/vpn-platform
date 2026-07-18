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
}

type Consumer struct {
	client     consumerClient
	processor  Processor
	store      deadLetterStore
	logger     *zap.Logger
	retryDelay time.Duration
}

func NewConsumer(client consumerClient, processor Processor, store deadLetterStore, logger *zap.Logger, retryDelay time.Duration) *Consumer {
	return &Consumer{client: client, processor: processor, store: store, logger: logger, retryDelay: retryDelay}
}

func (c *Consumer) Run(ctx context.Context) {
	for c.pollOnce(ctx) {
	}
}

func (c *Consumer) pollOnce(ctx context.Context) bool {
	fetches := c.client.PollRecords(ctx, 1)
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
		err := c.process(ctx, record)
		if err != nil {
			if code, poison := application.ContractErrorCode(err); poison {
				sum := sha256.Sum256(record.Value)
				if deadErr := c.store.RecordDeadLetter(ctx, record.Topic, record.Partition, record.Offset, hex.EncodeToString(sum[:]), code); deadErr != nil {
					c.rewind(records[index:])
					c.waitRetry(ctx, deadErr)
					return ctx.Err() == nil
				}
			} else {
				c.rewind(records[index:])
				c.waitRetry(ctx, err)
				return ctx.Err() == nil
			}
		}
		commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.client.CommitRecords(commitCtx, record)
		cancel()
		if err != nil {
			c.rewind(records[index:])
			c.waitRetry(ctx, err)
			return ctx.Err() == nil
		}
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
		PartitionKey:  envelope.PartitionKey,
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

func (c *Consumer) waitRetry(ctx context.Context, err error) {
	c.logger.Warn("access Kafka record will retry", zap.String("error_type", fmt.Sprintf("%T", err)))
	timer := time.NewTimer(c.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
