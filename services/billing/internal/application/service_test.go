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
	orderReplay      bool
	paymentReplay    bool
	ambiguous        bool
	failed           bool
	ambiguousErr     error
	failedErr        error
	recoveryCtxErr   error
}

func (f *fakeStore) FindOrderReplay(context.Context, string, string, string) (domain.Order, bool, error) {
	return f.order, f.orderReplay, nil
}
func (f *fakeStore) CreateOrder(_ context.Context, input domain.CreateOrderInput) (domain.Order, bool, error) {
	f.createOrderInput = input
	return f.order, true, nil
}
func (f *fakeStore) GetOrder(context.Context, string, string) (domain.Order, error) {
	return f.order, nil
}
func (f *fakeStore) FindPaymentReplay(context.Context, string, string, string) (domain.PaymentOperation, bool, error) {
	return f.payment, f.paymentReplay, nil
}
func (f *fakeStore) CreatePayment(context.Context, domain.CreatePaymentInput, string, time.Duration) (domain.PaymentOperation, bool, error) {
	return f.payment, true, nil
}
func (f *fakeStore) PrepareProviderCreate(_ context.Context, _ string, observedAt time.Time) (domain.PaymentOperation, bool, error) {
	if !observedAt.Before(f.payment.ProviderCreateDeadline) {
		f.failed = true
		f.payment.Status = domain.PaymentStatusFailed
		return f.payment, false, nil
	}
	return f.payment, true, nil
}
func (f *fakeStore) MarkProviderCreateAmbiguous(ctx context.Context, _ string, _ string, _ time.Duration) error {
	f.recoveryCtxErr = ctx.Err()
	if f.ambiguousErr != nil {
		return f.ambiguousErr
	}
	f.ambiguous = true
	f.payment.Status = domain.PaymentStatusVerificationPending
	return nil
}
func (f *fakeStore) MarkProviderCreateFailed(ctx context.Context, _ string, _ string) error {
	f.recoveryCtxErr = ctx.Err()
	if f.failedErr != nil {
		return f.failedErr
	}
	f.failed = true
	f.payment.Status = domain.PaymentStatusFailed
	return nil
}
func (f *fakeStore) GetPayment(context.Context, string) (domain.PaymentOperation, error) {
	return f.payment, nil
}
func (f *fakeStore) ApplyProviderCreate(ctx context.Context, _ string, p domain.ProviderPayment) (domain.PaymentOperation, error) {
	f.recoveryCtxErr = ctx.Err()
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
	calls   *int
	cancel  context.CancelFunc
}

func (f fakeProvider) CreatePayment(context.Context, domain.ProviderCreateRequest) (domain.ProviderPayment, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.cancel != nil {
		f.cancel()
	}
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

func TestCreateOrderExactReplayDoesNotDependOnIdentityOrCatalog(t *testing.T) {
	order := domain.Order{OrderID: "order-id", UserID: "user-id"}
	store := &fakeStore{order: order, orderReplay: true}
	service := NewService(store, fakeIdentity{err: domain.ErrUserUnavailable}, fakeCatalog{err: domain.ErrNotFound}, fakeProvider{}, "shop", 23*time.Hour, time.Second)

	replayed, created, err := service.CreateOrder(context.Background(), order.UserID, "plan", "region", "terms", "order-idem")
	if err != nil || created || replayed.OrderID != order.OrderID {
		t.Fatalf("order=%+v created=%v err=%v", replayed, created, err)
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

func TestCreatePaymentPersistsAmbiguousResultAfterRequestCancellation(t *testing.T) {
	operation := testOperation()
	store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation}
	providerErr := &domain.ProviderError{Kind: "ambiguous", Code: "http_500"}
	ctx, cancel := context.WithCancel(context.Background())
	service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{err: providerErr, cancel: cancel}, "shop", 23*time.Hour, time.Second)

	payment, _, err := service.CreatePayment(ctx, operation.UserID, operation.OrderID, "payment-idem")
	if err != nil {
		t.Fatal(err)
	}
	if !store.ambiguous || store.recoveryCtxErr != nil || payment.Status != domain.PaymentStatusVerificationPending {
		t.Fatalf("payment=%+v ambiguous=%v recovery_ctx_err=%v", payment, store.ambiguous, store.recoveryCtxErr)
	}
}

func TestCreatePaymentPersistsProviderSuccessAfterRequestCancellation(t *testing.T) {
	operation := testOperation()
	store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation}
	confirmationURL := "https://example.invalid/confirm"
	providerPayment := domain.ProviderPayment{ProviderPaymentID: "provider-id", Status: "pending", AmountMinor: operation.AmountMinor, Currency: operation.Currency, ConfirmationURL: &confirmationURL, Test: true, AccountID: "shop", MetadataOrderID: operation.OrderID, MetadataPaymentID: operation.PaymentID}
	ctx, cancel := context.WithCancel(context.Background())
	service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{payment: providerPayment, cancel: cancel}, "shop", 23*time.Hour, time.Second)

	payment, _, err := service.CreatePayment(ctx, operation.UserID, operation.OrderID, "payment-idem")
	if err != nil {
		t.Fatal(err)
	}
	if store.recoveryCtxErr != nil || payment.Status != domain.PaymentStatusPending {
		t.Fatalf("payment=%+v recovery_ctx_err=%v", payment, store.recoveryCtxErr)
	}
}

func TestCreatePaymentReturnsAmbiguousPersistenceError(t *testing.T) {
	operation := testOperation()
	persistErr := errors.New("database unavailable")
	store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation, ambiguousErr: persistErr}
	providerErr := &domain.ProviderError{Kind: "ambiguous", Code: "http_500"}
	service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{err: providerErr}, "shop", 23*time.Hour, time.Second)

	_, _, err := service.CreatePayment(context.Background(), operation.UserID, operation.OrderID, "payment-idem")
	if !errors.Is(err, persistErr) || !errors.Is(err, providerErr) {
		t.Fatalf("error=%v", err)
	}
}

func TestCreatePaymentDoesNotCallProviderAtOrAfterCreateDeadline(t *testing.T) {
	deadline := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for name, observedAt := range map[string]time.Time{"at_23h_deadline": deadline, "after_24h_provider_window": deadline.Add(time.Hour)} {
		t.Run(name, func(t *testing.T) {
			operation := testOperation()
			operation.ProviderCreateDeadline = deadline
			store := &fakeStore{order: domain.Order{OrderID: operation.OrderID, UserID: operation.UserID, Status: domain.OrderStatusCreated, AcceptedTermsVersion: "terms-v1"}, payment: operation}
			calls := 0
			service := NewService(store, fakeIdentity{}, fakeCatalog{}, fakeProvider{calls: &calls}, "shop", 23*time.Hour, time.Second)
			service.now = func() time.Time { return observedAt }

			payment, _, err := service.CreatePayment(context.Background(), operation.UserID, operation.OrderID, "payment-idem")
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 || payment.Status != domain.PaymentStatusFailed || !store.failed {
				t.Fatalf("calls=%d payment=%+v failed=%v", calls, payment, store.failed)
			}
		})
	}
}

func TestCreatePaymentExactReplayDoesNotDependOnIdentity(t *testing.T) {
	operation := testOperation()
	operation.Status = domain.PaymentStatusPending
	store := &fakeStore{payment: operation, paymentReplay: true}
	service := NewService(store, fakeIdentity{err: domain.ErrUserUnavailable}, fakeCatalog{}, fakeProvider{}, "shop", 23*time.Hour, time.Second)

	payment, created, err := service.CreatePayment(context.Background(), operation.UserID, operation.OrderID, "payment-idem")
	if err != nil || created || payment.PaymentID != operation.PaymentID {
		t.Fatalf("payment=%+v created=%v err=%v", payment, created, err)
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
