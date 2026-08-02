package kafka

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestValidateEnvelopeRejectsMismatchedKafkaKey(t *testing.T) {
	envelope := validEnvelope()
	record := &kgo.Record{Topic: envelope.EventType, Key: []byte("credential:018f0e61-bca5-7a40-a06f-e4c0f53128ae")}
	if err := validateEnvelope(record, envelope); err == nil {
		t.Fatal("mismatched Kafka key was accepted")
	}
}

func TestStrictDecodeRejectsUnknownEnvelopeField(t *testing.T) {
	payload := []byte(`{"event_id":"018f0e61-bca5-7a40-a06f-e4c0f53128ad","unknown":true}`)
	var envelope platformkafka.Envelope
	if err := strictDecode(payload, &envelope); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestValidateProvisionEnvelope(t *testing.T) {
	envelope := validEnvelope()
	record := &kgo.Record{Topic: envelope.EventType, Key: []byte(envelope.PartitionKey)}
	if err := validateEnvelope(record, envelope); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProvisionOutcomeEnvelopeRequiresAggregateSequence(t *testing.T) {
	envelope := validEnvelope()
	envelope.AggregateSequence = 0
	record := &kgo.Record{Topic: envelope.EventType, Key: []byte(envelope.PartitionKey)}
	if err := validateEnvelope(record, envelope); err == nil {
		t.Fatal("provisioning outcome without aggregate sequence was accepted")
	}
	envelope.AggregateSequence = 1
	if err := validateEnvelope(record, envelope); err != nil {
		t.Fatalf("sequenced provisioning outcome was rejected: %v", err)
	}
}

func TestValidateLifecycleEnvelopeRequiresAggregateSequence(t *testing.T) {
	envelope := validEnvelope()
	envelope.AggregateSequence = 0
	envelope.EventType = "subscription.activated.v1"
	envelope.Producer = "subscription-service"
	envelope.AggregateType = "subscription"
	envelope.AggregateID = "018f0e61-bca5-7a40-a06f-e4c0f53128af"
	envelope.PartitionKey = "user:018f0e61-bca5-7a40-a06f-e4c0f53128ac"
	record := &kgo.Record{Topic: envelope.EventType, Key: []byte(envelope.PartitionKey)}
	if err := validateEnvelope(record, envelope); err == nil {
		t.Fatal("lifecycle envelope without aggregate sequence was accepted")
	}
	envelope.AggregateSequence = 1
	if err := validateEnvelope(record, envelope); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredSequenceGapResumesAfterMissingEventIsApplied(t *testing.T) {
	client := &fakeConsumerClient{}
	processor := &gapProcessor{}
	consumer := NewConsumer(client, processor, fakeDeadLetterStore{}, zap.NewNop(), time.Millisecond)
	envelope := validEnvelope()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	record := &kgo.Record{Topic: envelope.EventType, Partition: 2, Offset: 9, Key: []byte(envelope.PartitionKey), Value: payload}
	consumer.deferGap(record)
	if !client.paused || len(consumer.deferred) != 1 {
		t.Fatal("gap partition was not deferred and paused")
	}
	processor.resolved = true
	consumer.retryDeferred(context.Background())
	if !client.resumed || client.commits != 1 || len(consumer.deferred) != 0 {
		t.Fatalf("deferred record was not committed and resumed: resumed=%t commits=%d pending=%d", client.resumed, client.commits, len(consumer.deferred))
	}
}

func validEnvelope() platformkafka.Envelope {
	data, _ := json.Marshal(map[string]any{"operation_id": "018f0e61-bca5-7a40-a06f-e4c0f53128ae"})
	return platformkafka.Envelope{
		EventID: "018f0e61-bca5-7a40-a06f-e4c0f53128ad", EventType: "access.provision.succeeded.v1", SchemaVersion: 1,
		OccurredAt: time.Now().UTC(), Producer: "provisioning-service", CorrelationID: "018f0e61-bca5-7a40-a06f-e4c0f53128ae",
		AggregateType: "credential", AggregateID: "018f0e61-bca5-7a40-a06f-e4c0f53128af",
		AggregateSequence: 1, PartitionKey: "credential:018f0e61-bca5-7a40-a06f-e4c0f53128af", Data: data,
	}
}

type gapProcessor struct{ resolved bool }

func (p *gapProcessor) ProcessEvent(context.Context, domain.EventMeta, json.RawMessage) error {
	if !p.resolved {
		return domain.ErrLifecycleSequenceGap
	}
	return nil
}

type fakeDeadLetterStore struct{}

func (fakeDeadLetterStore) RecordDeadLetter(context.Context, string, int32, int64, string, string) error {
	return nil
}

type fakeConsumerClient struct {
	paused  bool
	resumed bool
	commits int
}

func (f *fakeConsumerClient) PollRecords(context.Context, int) kgo.Fetches { return nil }
func (f *fakeConsumerClient) AllowRebalance()                              {}
func (f *fakeConsumerClient) CommitRecords(context.Context, ...*kgo.Record) error {
	f.commits++
	return nil
}
func (f *fakeConsumerClient) SetOffsets(map[string]map[int32]kgo.EpochOffset) {}
func (f *fakeConsumerClient) PauseFetchPartitions(map[string][]int32) map[string][]int32 {
	f.paused = true
	return nil
}
func (f *fakeConsumerClient) ResumeFetchPartitions(map[string][]int32) { f.resumed = true }
