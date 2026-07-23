package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

func TestProcessEventUsesStableBusinessDedupeAndPolicy(t *testing.T) {
	store := &captureStore{}
	service := NewService(store, 8)
	meta := validMeta("subscription.activated.v1", 1)
	payload := json.RawMessage(`{"subscription_id":"11111111-1111-4111-8111-111111111111","user_id":"22222222-2222-4222-8222-222222222222","period_id":"33333333-3333-4333-8333-333333333333","source_order_id":"44444444-4444-4444-8444-444444444444","source_payment_id":"55555555-5555-4555-8555-555555555555","period_start":"2026-07-19T12:00:00Z","period_end":"2026-08-18T12:00:00Z","grace_ends_at":"2026-08-19T12:00:00Z"}`)
	if err := service.ProcessEvent(context.Background(), meta, payload); err != nil {
		t.Fatal(err)
	}
	first := store.intent
	if err := service.ProcessEvent(context.Background(), meta, payload); err != nil {
		t.Fatal(err)
	}
	if first.BusinessDedupeKey != store.intent.BusinessDedupeKey || first.NotificationID == store.intent.NotificationID {
		t.Fatalf("dedupe=%q/%q notification=%q/%q", first.BusinessDedupeKey, store.intent.BusinessDedupeKey, first.NotificationID, store.intent.NotificationID)
	}
	if store.intent.SuppressedReason != "covered_by_payment_and_ready" {
		t.Fatalf("activation suppression = %q", store.intent.SuppressedReason)
	}
	if store.intent.DeliveryStreamKey != "subscription:11111111-1111-4111-8111-111111111111" || store.intent.DeliverySequence != 1 {
		t.Fatalf("delivery order = %q/%d", store.intent.DeliveryStreamKey, store.intent.DeliverySequence)
	}
}

func TestRefundSupersedesPaymentNotificationStream(t *testing.T) {
	store := &captureStore{}
	service := NewService(store, 8)
	meta := domain.EventMeta{
		EventID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EventType: "billing.refund.succeeded.v1",
		Producer: "billing-service", AggregateType: "refund", AggregateID: "11111111-1111-4111-8111-111111111111",
		PartitionKey:  "user:22222222-2222-4222-8222-222222222222",
		CorrelationID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", OccurredAt: time.Now().UTC(),
		SourceTopic: "billing.refund.succeeded.v1", PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	payload := json.RawMessage(`{"refund_id":"11111111-1111-4111-8111-111111111111","payment_id":"33333333-3333-4333-8333-333333333333","order_id":"44444444-4444-4444-8444-444444444444","user_id":"22222222-2222-4222-8222-222222222222","amount_minor":49900,"currency":"RUB","refund_scope":"full","refunded_at":"2026-07-19T12:00:00Z"}`)
	if err := service.ProcessEvent(context.Background(), meta, payload); err != nil {
		t.Fatal(err)
	}
	if store.intent.DeliveryStreamKey != "payment:33333333-3333-4333-8333-333333333333" ||
		store.intent.DeliverySequence != 2 || !store.intent.SupersedesOlder {
		t.Fatalf("refund delivery policy = %+v", store.intent)
	}
}

func TestRefundRequiresPaymentForDeliveryBarrier(t *testing.T) {
	service := NewService(&captureStore{}, 8)
	meta := domain.EventMeta{
		EventID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EventType: "billing.refund.succeeded.v1",
		Producer: "billing-service", AggregateType: "refund", AggregateID: "11111111-1111-4111-8111-111111111111",
		PartitionKey:  "user:22222222-2222-4222-8222-222222222222",
		CorrelationID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", OccurredAt: time.Now().UTC(),
		SourceTopic: "billing.refund.succeeded.v1", PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	payload := json.RawMessage(`{"refund_id":"11111111-1111-4111-8111-111111111111","payment_id":"","order_id":"44444444-4444-4444-8444-444444444444","user_id":"22222222-2222-4222-8222-222222222222","amount_minor":49900,"currency":"RUB","refund_scope":"full","refunded_at":"2026-07-19T12:00:00Z"}`)
	if err := service.ProcessEvent(context.Background(), meta, payload); err == nil {
		t.Fatal("refund without payment_id was accepted")
	}
}

func TestProcessEventRejectsPartitionUserMismatch(t *testing.T) {
	service := NewService(&captureStore{}, 8)
	meta := validMeta("subscription.grace.started.v1", 2)
	meta.PartitionKey = "user:99999999-9999-4999-8999-999999999999"
	payload := json.RawMessage(`{"subscription_id":"11111111-1111-4111-8111-111111111111","user_id":"22222222-2222-4222-8222-222222222222","period_end":"2026-08-18T12:00:00Z","grace_ends_at":"2026-08-19T12:00:00Z","effective_at":"2026-08-18T12:00:00Z"}`)
	if err := service.ProcessEvent(context.Background(), meta, payload); err == nil {
		t.Fatal("partition mismatch accepted")
	}
}

type captureStore struct {
	domain.Store
	intent domain.Intent
}

func (s *captureStore) RecordEvent(_ context.Context, _ domain.EventMeta, intent domain.Intent) (bool, error) {
	s.intent = intent
	return true, nil
}

func validMeta(eventType string, sequence int64) domain.EventMeta {
	return domain.EventMeta{
		EventID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EventType: eventType,
		Producer: "subscription-service", AggregateType: "subscription",
		AggregateID: "11111111-1111-4111-8111-111111111111", AggregateSequence: sequence,
		PartitionKey:  "user:22222222-2222-4222-8222-222222222222",
		CorrelationID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", OccurredAt: time.Now().UTC(),
		SourceTopic: eventType, PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}
