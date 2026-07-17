package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type fakeStore struct {
	domain.Store
	order            domain.Order
	payment          domain.PaymentOperation
	createOrderInput domain.CreateOrderInput
	ambiguous        bool
	failed           bool
}

func (f *fakeStore) CreateOrder(_ context.Context, input domain.CreateOrderInput) (domain.Order, bool, error) {
	f.createOrderInput = input
	return f.order, true, nil
}
func (f *fakeStore) GetOrder(context.Context, string, string) (domain.Order, error) {
	return f.order, nil
}
func (f *fakeStore) CreatePayment(context.Context, domain.CreatePaymentInput, string, time.Duration) (domain.PaymentOperation, bool, error) {
	return f.payment, true, nil
}
func (f *fakeStore) MarkProviderCreateAmbiguous(context.Context, string, string, time.Duration) error {
	f.ambiguous = true
	f.payment.Status = domain.PaymentStatusVerificationPending
	return nil
}
func (f *fakeStore) MarkProviderCreateFailed(context.Context, string, string) error {
	f.failed = true
	return nil
}
func (f *fakeStore) GetPayment(context.Context, string) (domain.PaymentOperation, error) {
	return f.payment, nil
}
func (f *fakeStore) ApplyProviderCreate(_ context.Context, _ string, p domain.ProviderPayment) (domain.PaymentOperation, error) {
	f.payment.ProviderPaymentID = &p.ProviderPaymentID
	f.payment.Status = domain.PaymentStatusPending
	f.payment.ConfirmationURL = p.ConfirmationURL
	return f.payment, nil
}

type fakeIdentity struct{ err error }

func (f fakeIdentity) VerifyActiveWithConsent(context.Context, string, string) error { return f.err }

type fakeCatalog struct {
	plan domain.PlanSnapshot
	err  error
}

func (f fakeCatalog) GetPlan(context.Context, string, string) (domain.PlanSnapshot, error) {
	return f.plan, f.err
}

type fakeProvider struct {
	payment domain.ProviderPayment
	err     error
}

func (f fakeProvider) CreatePayment(context.Context, domain.ProviderCreateRequest) (domain.ProviderPayment, error) {
	return f.payment, f.err
}
func (f fakeProvider) GetPayment(context.Context, string) (domain.ProviderPayment, error) {
	return f.payment, f.err
}

func TestCreateOrderSnapshotsCatalogPlan(t *testing.T) {
	plan := domain.PlanSnapshot{PlanID: "vpn-30d-v1", Name: "VPN 30 days", DurationDays: 30, GracePeriodHours: 24, AmountMinor: 29900, Currency: "RUB", Region: "ru-test", RegionPolicy: "single_region_with_failover", TrafficPolicy: "no_hard_cap", PrimaryNodes: 1, FailoverNodes: 1}
	store := &fakeStore{order: domain.Order{OrderID: "order-id"}}
	service := NewService(store, fakeIdentity{}, fakeCatalog{plan: plan}, fakeProvider{}, "shop", 23*time.Hour, time.Second)
	_, _, err := service.CreateOrder(context.Background(), "user-id", "vpn-30d-v1", "ru-test", "terms-v1", "idem-key")
	if err != nil {
		t.Fatal(err)
	}
	if store.createOrderInput.Snapshot != plan || store.createOrderInput.RequestHash == "" {
		t.Fatalf("unexpected input: %+v", store.createOrderInput)
	}
}

func TestCreatePaymentKeepsAmbiguousProviderResultRecoverable(t *testing.T) {
	operation := testOperation()
	store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation}
	providerErr := &domain.ProviderError{Kind: "ambiguous", Code: "http_500"}
	service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{err: providerErr}, "shop", 23*time.Hour, time.Second)
	payment, _, err := service.CreatePayment(context.Background(), operation.UserID, operation.OrderID, "payment-idem")
	if err != nil {
		t.Fatal(err)
	}
	if !store.ambiguous || payment.Status != domain.PaymentStatusVerificationPending {
		t.Fatalf("payment=%+v ambiguous=%v", payment, store.ambiguous)
	}
}

func TestCreatePaymentRejectsProviderMismatch(t *testing.T) {
	operation := testOperation()
	store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation}
	providerPayment := domain.ProviderPayment{ProviderPaymentID: "provider-id", Status: "pending", AmountMinor: 1, Currency: "RUB", Test: true, AccountID: "shop", MetadataOrderID: operation.OrderID, MetadataPaymentID: operation.PaymentID}
	service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{payment: providerPayment}, "shop", 23*time.Hour, time.Second)
	_, _, err := service.CreatePayment(context.Background(), operation.UserID, operation.OrderID, "payment-idem")
	if err == nil || !store.failed {
		t.Fatalf("err=%v failed=%v", err, store.failed)
	}
}

func TestCreateOrderFailsClosedForBlockedUser(t *testing.T) {
	service := NewService(&fakeStore{}, fakeIdentity{err: domain.ErrUserUnavailable}, fakeCatalog{}, fakeProvider{}, "shop", 23*time.Hour, time.Second)
	_, _, err := service.CreateOrder(context.Background(), "user", "plan", "region", "terms", "idempotency")
	if !errors.Is(err, domain.ErrUserUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func testOperation() domain.PaymentOperation {
	return domain.PaymentOperation{Payment: domain.Payment{PaymentID: "payment-id", OrderID: "order-id", Status: domain.PaymentStatusCreated, AmountMinor: 29900, Currency: "RUB"}, UserID: "user-id", PlanID: "vpn-30d-v1", Provider: "yookassa", ProviderIdempotencyKey: "provider-idem", ProviderCreateDeadline: time.Now().Add(time.Hour)}
}
