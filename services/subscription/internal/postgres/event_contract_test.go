package postgres

import (
	"encoding/json"
	"testing"
	"time"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
)

func TestBuildSubscriptionPeriodEventContract(t *testing.T) {
	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	causationID := "11111111-1111-4111-8111-111111111111"
	data := periodEventData{
		SubscriptionID:  "22222222-2222-4222-8222-222222222222",
		UserID:          "33333333-3333-4333-8333-333333333333",
		PeriodID:        "44444444-4444-4444-8444-444444444444",
		SourceOrderID:   "55555555-5555-4555-8555-555555555555",
		SourcePaymentID: "66666666-6666-4666-8666-666666666666",
		PeriodStart:     start, PeriodEnd: start.Add(30 * 24 * time.Hour), GraceEndsAt: start.Add(31 * 24 * time.Hour),
	}
	payload, err := buildEventPayload("subscription.activated.v1", "77777777-7777-4777-8777-777777777777", data.SubscriptionID, data.UserID, "88888888-8888-4888-8888-888888888888", &causationID, 1, start, data)
	if err != nil {
		t.Fatal(err)
	}
	var envelope platformkafka.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.Producer != "subscription-service" || envelope.AggregateType != "subscription" || envelope.AggregateID != data.SubscriptionID || envelope.AggregateSequence != 1 || envelope.PartitionKey != "user:"+data.UserID {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	var decoded periodEventData
	if err := json.Unmarshal(envelope.Data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != data {
		t.Fatalf("event data=%+v, want %+v", decoded, data)
	}
}

func TestBuildSubscriptionTerminalEventContract(t *testing.T) {
	effectiveAt := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	data := terminalEventData{SubscriptionID: "22222222-2222-4222-8222-222222222222", UserID: "33333333-3333-4333-8333-333333333333", Reason: "expired", EffectiveAt: effectiveAt}
	payload, err := buildEventPayload("subscription.expired.v1", "77777777-7777-4777-8777-777777777777", data.SubscriptionID, data.UserID, "", nil, 1, effectiveAt, data)
	if err != nil {
		t.Fatal(err)
	}
	var envelope platformkafka.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.CorrelationID != envelope.EventID || envelope.CausationID != nil || envelope.EventType != "subscription.expired.v1" || envelope.AggregateSequence != 1 {
		t.Fatalf("unexpected terminal envelope: %+v", envelope)
	}
	var decoded terminalEventData
	if err := json.Unmarshal(envelope.Data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SubscriptionID != data.SubscriptionID || decoded.UserID != data.UserID || decoded.Reason != "expired" || !decoded.EffectiveAt.Equal(effectiveAt) {
		t.Fatalf("terminal data=%+v", decoded)
	}
}
