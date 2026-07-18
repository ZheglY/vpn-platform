package kafka

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
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

func validEnvelope() platformkafka.Envelope {
	data, _ := json.Marshal(map[string]any{"operation_id": "018f0e61-bca5-7a40-a06f-e4c0f53128ae"})
	return platformkafka.Envelope{
		EventID: "018f0e61-bca5-7a40-a06f-e4c0f53128ad", EventType: "access.provision.succeeded.v1", SchemaVersion: 1,
		OccurredAt: time.Now().UTC(), Producer: "provisioning-service", CorrelationID: "018f0e61-bca5-7a40-a06f-e4c0f53128ae",
		AggregateType: "credential", AggregateID: "018f0e61-bca5-7a40-a06f-e4c0f53128af",
		PartitionKey: "credential:018f0e61-bca5-7a40-a06f-e4c0f53128af", Data: data,
	}
}
