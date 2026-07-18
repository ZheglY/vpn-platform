package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

const (
	testEventID       = "11111111-1111-4111-8111-111111111111"
	testCorrelationID = "22222222-2222-4222-8222-222222222222"
	testPaymentID     = "33333333-3333-4333-8333-333333333333"
	testOrderID       = "44444444-4444-4444-8444-444444444444"
	testUserID        = "55555555-5555-4555-8555-555555555555"
)

func TestHandlePaymentReplayDoesNotCallBilling(t *testing.T) {
	store := &fakeStore{replayed: true}
	billing := &fakeBilling{}
	service := NewService(store, billing)
	if err := service.HandlePayment(context.Background(), validPaymentMeta(), validPayment()); err != nil {
		t.Fatal(err)
	}
	if billing.calls != 0 || store.applyCalls != 0 {
		t.Fatalf("billing calls=%d apply calls=%d", billing.calls, store.applyCalls)
	}
}

func TestHandlePaymentRejectsBillingSnapshotMismatch(t *testing.T) {
	store := &fakeStore{}
	billing := &fakeBilling{order: validOrder()}
	billing.order.AmountMinor++
	service := NewService(store, billing)
	err := service.HandlePayment(context.Background(), validPaymentMeta(), validPayment())
	if code, ok := ContractErrorCode(err); !ok || code != "billing_order_mismatch" {
		t.Fatalf("error=%v code=%q poison=%v", err, code, ok)
	}
	if store.applyCalls != 0 {
		t.Fatal("mismatched order was applied")
	}
}

func TestHandlePaymentAppliesValidatedSnapshot(t *testing.T) {
	store := &fakeStore{}
	billing := &fakeBilling{order: validOrder()}
	service := NewService(store, billing)
	if err := service.HandlePayment(context.Background(), validPaymentMeta(), validPayment()); err != nil {
		t.Fatal(err)
	}
	if billing.calls != 1 || store.applyCalls != 1 {
		t.Fatalf("billing calls=%d apply calls=%d", billing.calls, store.applyCalls)
	}
}

func TestHandleRefundRejectsPartialScope(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, &fakeBilling{})
	refund := domain.RefundSucceeded{RefundID: testEventID, PaymentID: testPaymentID, OrderID: testOrderID, UserID: testUserID, AmountMinor: 29900, Currency: "RUB", RefundScope: "partial", RefundedAt: time.Now().UTC()}
	meta := domain.EventMeta{EventID: testCorrelationID, EventType: "billing.refund.succeeded.v1", AggregateID: refund.RefundID, CorrelationID: testOrderID, SourceTopic: "billing.refund.succeeded.v1", SourcePartition: 0, SourceOffset: 1, PayloadSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	if err := service.HandleRefund(context.Background(), meta, refund); err == nil {
		t.Fatal("partial refund was accepted")
	}
}

type fakeStore struct {
	domain.Store
	replayed    bool
	replayErr   error
	applyCalls  int
	refundCalls int
}

func (s *fakeStore) RecordPaymentReplay(context.Context, domain.EventMeta, domain.PaymentSucceeded) (bool, error) {
	return s.replayed, s.replayErr
}

func (s *fakeStore) ApplyPayment(context.Context, domain.EventMeta, domain.PaymentSucceeded, domain.Order) error {
	s.applyCalls++
	return nil
}

func (s *fakeStore) StoreRefund(context.Context, domain.EventMeta, domain.RefundSucceeded) error {
	s.refundCalls++
	return nil
}

type fakeBilling struct {
	domain.Billing
	order domain.Order
	err   error
	calls int
}

func (b *fakeBilling) GetOrder(context.Context, string, string) (domain.Order, error) {
	b.calls++
	return b.order, b.err
}

func validPaymentMeta() domain.EventMeta {
	return domain.EventMeta{EventID: testEventID, EventType: "billing.payment.succeeded.v1", AggregateID: testPaymentID, CorrelationID: testCorrelationID, OccurredAt: testOccurredAt(), SourceTopic: "billing.payment.succeeded.v1", SourcePartition: 0, SourceOffset: 1, PayloadSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
}

func validPayment() domain.PaymentSucceeded {
	return domain.PaymentSucceeded{PaymentID: testPaymentID, OrderID: testOrderID, UserID: testUserID, PlanID: "vpn-30d-v1", AmountMinor: 29900, Currency: "RUB", PaidAt: testOccurredAt()}
}

func testOccurredAt() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }

func validOrder() domain.Order {
	return domain.Order{OrderID: testOrderID, UserID: testUserID, Status: "paid", AmountMinor: 29900, Currency: "RUB", PlanSnapshot: domain.PlanSnapshot{PlanID: "vpn-30d-v1", DurationDays: 30, GracePeriodHours: 24, AmountMinor: 29900, Currency: "RUB", Region: "ru-test"}}
}

func TestContractErrorCodeRecognizesDurableConflict(t *testing.T) {
	code, ok := ContractErrorCode(errors.Join(errors.New("wrapped"), domain.ErrConflict))
	if !ok || code != "durable_state_conflict" {
		t.Fatalf("code=%q ok=%v", code, ok)
	}
}
