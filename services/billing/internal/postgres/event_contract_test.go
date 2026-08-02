package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

func TestBuildPaymentSucceededEventContract(t *testing.T) {
	paidAt := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	payment := domain.PaymentOperation{Payment: domain.Payment{PaymentID: "11111111-1111-4111-8111-111111111111", OrderID: "22222222-2222-4222-8222-222222222222", AmountMinor: 29900, Currency: "RUB"}, UserID: "33333333-3333-4333-8333-333333333333", PlanID: "vpn-30d-v1"}
	topic, occurredAt, payload, err := buildPaymentEvent(payment, domain.ProviderPayment{Status: "succeeded", CapturedAt: &paidAt}, "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if topic != "billing.payment.succeeded.v1" || !occurredAt.Equal(paidAt) {
		t.Fatalf("topic=%q occurred_at=%s", topic, occurredAt)
	}
	var envelope kafka.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.EventType != topic || envelope.PartitionKey != "user:"+payment.UserID || envelope.AggregateID != payment.PaymentID {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 7 || data["plan_id"] != payment.PlanID || data["currency"] != payment.Currency || data["amount_minor"] != float64(payment.AmountMinor) {
		t.Fatalf("unexpected event data: %+v", data)
	}
}

func TestBuildPaymentCanceledEventContract(t *testing.T) {
	canceledAt := time.Date(2026, 7, 17, 4, 5, 6, 0, time.UTC)
	payment := domain.PaymentOperation{Payment: domain.Payment{PaymentID: "11111111-1111-4111-8111-111111111111", OrderID: "22222222-2222-4222-8222-222222222222"}, UserID: "33333333-3333-4333-8333-333333333333"}
	topic, occurredAt, payload, err := buildPaymentEvent(payment, domain.ProviderPayment{Status: "canceled", CanceledAt: &canceledAt}, "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if topic != "billing.payment.canceled.v1" || !occurredAt.Equal(canceledAt) {
		t.Fatalf("topic=%q occurred_at=%s", topic, occurredAt)
	}
	var envelope kafka.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.EventType != topic || envelope.PartitionKey != "user:"+payment.UserID || envelope.AggregateID != payment.PaymentID {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 || data["order_id"] != payment.OrderID || data["user_id"] != payment.UserID || data["canceled_at"] != canceledAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected event data: %+v", data)
	}
}
