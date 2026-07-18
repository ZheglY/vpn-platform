package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

func TestIntegrationConcurrentPaymentKeysShareOnePayment(t *testing.T) {
	store := openIntegrationStore(t)
	userID, orderID := seedOrder(t, store, domain.OrderStatusCreated)
	start := make(chan struct{})
	results := make(chan struct {
		payment domain.PaymentOperation
		err     error
	}, 2)

	var wg sync.WaitGroup
	for _, key := range []string{"payment-key-a", "payment-key-b"} {
		wg.Add(1)
		go func(idempotencyKey string) {
			defer wg.Done()
			<-start
			payment, _, err := store.CreatePayment(context.Background(), domain.CreatePaymentInput{
				UserID: userID, OrderID: orderID, IdempotencyKey: idempotencyKey, RequestHash: strings.Repeat("a", 64),
			}, "yookassa", 23*time.Hour)
			results <- struct {
				payment domain.PaymentOperation
				err     error
			}{payment: payment, err: err}
		}(key)
	}
	close(start)
	wg.Wait()
	close(results)

	var paymentID string
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if paymentID == "" {
			paymentID = result.payment.PaymentID
		} else if result.payment.PaymentID != paymentID {
			t.Fatalf("concurrent requests returned different payments: %s and %s", paymentID, result.payment.PaymentID)
		}
	}
	assertCount(t, store, `SELECT count(*) FROM payments WHERE order_id=$1`, 1, orderID)
	assertCount(t, store, `SELECT count(*) FROM idempotency_keys WHERE resource_id=$1`, 2, paymentID)
}

func TestIntegrationDatabaseConstraintsAndStateTriggers(t *testing.T) {
	store := openIntegrationStore(t)
	_, orderID := seedOrder(t, store, domain.OrderStatusCreated)
	paymentID := seedPayment(t, store, orderID, domain.PaymentStatusCreated, time.Now().UTC().Add(23*time.Hour), nil)

	_, err := store.pool.Exec(context.Background(), `
INSERT INTO payments (id,order_id,provider,provider_idempotency_key,status,amount_minor,currency,provider_create_deadline)
VALUES ($1,$2,'yookassa',$3,'created',29900,'RUB',now()+interval '23 hours')`, newUUID(t), orderID, newUUID(t))
	assertPostgresCode(t, err, "23505")

	if _, err := store.pool.Exec(context.Background(), `UPDATE payments SET status='succeeded' WHERE id=$1`, paymentID); err == nil {
		t.Fatal("created payment transitioned directly to succeeded")
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE payments SET amount_minor=1 WHERE id=$1`, paymentID); err == nil {
		t.Fatal("payment amount was mutable")
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE orders SET status='paid' WHERE id=$1`, orderID); err == nil {
		t.Fatal("created order transitioned directly to paid")
	}
}

func TestIntegrationVerifiedPaymentAndOutboxAreAtomic(t *testing.T) {
	store := openIntegrationStore(t)
	_, orderID := seedOrder(t, store, domain.OrderStatusPaymentPending)
	confirmationURL := "https://example.invalid/confirm"
	paymentID := seedPayment(t, store, orderID, domain.PaymentStatusPending, time.Now().UTC().Add(23*time.Hour), &confirmationURL)
	operation, err := store.GetPayment(context.Background(), paymentID)
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Now().UTC().Truncate(time.Microsecond)
	provider := verifiedProviderPayment(operation, "succeeded", capturedAt)
	if err := store.ApplyVerifiedPayment(context.Background(), operation, provider, newUUID(t)); err != nil {
		t.Fatal(err)
	}

	var paymentStatus, orderStatus string
	var storedConfirmation *string
	if err := store.pool.QueryRow(context.Background(), `SELECT p.status,o.status,p.confirmation_url FROM payments p JOIN orders o ON o.id=p.order_id WHERE p.id=$1`, paymentID).Scan(&paymentStatus, &orderStatus, &storedConfirmation); err != nil {
		t.Fatal(err)
	}
	if paymentStatus != domain.PaymentStatusSucceeded || orderStatus != domain.OrderStatusPaid || storedConfirmation != nil {
		t.Fatalf("payment=%s order=%s confirmation=%v", paymentStatus, orderStatus, storedConfirmation)
	}
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE aggregate_id=$1 AND topic='billing.payment.succeeded.v1'`, 1, paymentID)

	resetBilling(t, store)
	_, orderID = seedOrder(t, store, domain.OrderStatusCanceled)
	paymentID = seedPayment(t, store, orderID, domain.PaymentStatusPending, time.Now().UTC().Add(23*time.Hour), &confirmationURL)
	operation, err = store.GetPayment(context.Background(), paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyVerifiedPayment(context.Background(), operation, verifiedProviderPayment(operation, "succeeded", capturedAt), newUUID(t)); err == nil {
		t.Fatal("terminal update succeeded despite an invalid order transition")
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id=$1`, paymentID).Scan(&paymentStatus); err != nil {
		t.Fatal(err)
	}
	if paymentStatus != domain.PaymentStatusPending {
		t.Fatalf("payment update was not rolled back: %s", paymentStatus)
	}
	assertCount(t, store, `SELECT count(*) FROM outbox`, 0)
}

func TestIntegrationReconcileLeasesSeparateWorkersAndRecover(t *testing.T) {
	store := openIntegrationStore(t)
	for range 2 {
		_, orderID := seedOrder(t, store, domain.OrderStatusPaymentPending)
		seedPayment(t, store, orderID, domain.PaymentStatusPending, time.Now().UTC().Add(23*time.Hour), nil)
	}

	start := make(chan struct{})
	claimed := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			payment, ok, err := store.ClaimPaymentForReconcile(context.Background(), time.Minute)
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				errs <- errors.New("worker did not claim a payment")
				return
			}
			claimed <- payment.PaymentID
		}()
	}
	close(start)
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	ids := map[string]struct{}{}
	for id := range claimed {
		ids[id] = struct{}{}
	}
	if len(ids) != 2 {
		t.Fatalf("workers did not receive distinct leases: %v", ids)
	}

	var reclaimID string
	for id := range ids {
		reclaimID = id
		break
	}
	if _, err := store.pool.Exec(context.Background(), `UPDATE payments SET reconcile_lease_until=now()-interval '1 second' WHERE id=$1`, reclaimID); err != nil {
		t.Fatal(err)
	}
	reclaimed, ok, err := store.ClaimPaymentForReconcile(context.Background(), time.Minute)
	if err != nil || !ok || reclaimed.PaymentID != reclaimID {
		t.Fatalf("reclaimed=%+v ok=%v err=%v", reclaimed, ok, err)
	}
}

func TestIntegrationProviderCreateDeadlineBoundary(t *testing.T) {
	store := openIntegrationStore(t)
	_, orderID := seedOrder(t, store, domain.OrderStatusPaymentPending)
	deadline := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	paymentID := seedPayment(t, store, orderID, domain.PaymentStatusCreated, deadline, nil)

	payment, allowed, err := store.PrepareProviderCreate(context.Background(), paymentID, deadline.Add(-time.Nanosecond))
	if err != nil || !allowed || payment.Status != domain.PaymentStatusCreated {
		t.Fatalf("before deadline payment=%+v allowed=%v err=%v", payment, allowed, err)
	}
	payment, allowed, err = store.PrepareProviderCreate(context.Background(), paymentID, deadline)
	if err != nil || allowed || payment.Status != domain.PaymentStatusFailed {
		t.Fatalf("at deadline payment=%+v allowed=%v err=%v", payment, allowed, err)
	}
	var orderStatus string
	if err := store.pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id=$1`, orderID).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if orderStatus != domain.OrderStatusCanceled {
		t.Fatalf("expired order status=%s", orderStatus)
	}
	payment, allowed, err = store.PrepareProviderCreate(context.Background(), paymentID, deadline.Add(time.Hour))
	if err != nil || allowed || payment.Status != domain.PaymentStatusFailed {
		t.Fatalf("after provider window payment=%+v allowed=%v err=%v", payment, allowed, err)
	}
}

func openIntegrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("BILLING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BILLING_TEST_DATABASE_URL is not set")
	}
	store, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	resetBilling(t, store)
	t.Cleanup(func() {
		resetBilling(t, store)
		store.Close()
	})
	return store
}

func resetBilling(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE outbox,webhook_inbox,idempotency_keys,payments,orders`); err != nil {
		t.Fatal(err)
	}
}

func seedOrder(t *testing.T, store *Store, status string) (string, string) {
	t.Helper()
	userID, orderID := newUUID(t), newUUID(t)
	_, err := store.pool.Exec(context.Background(), `
INSERT INTO orders (id,user_id,status,amount_minor,currency,plan_id,region,accepted_terms_version,plan_snapshot)
VALUES ($1,$2,$3,29900,'RUB','vpn-30d-v1','ru-test','terms-v1','{"plan_id":"vpn-30d-v1"}')`, orderID, userID, status)
	if err != nil {
		t.Fatal(err)
	}
	return userID, orderID
}

func seedPayment(t *testing.T, store *Store, orderID, status string, deadline time.Time, confirmationURL *string) string {
	t.Helper()
	paymentID := newUUID(t)
	providerID := any(nil)
	if status == domain.PaymentStatusPending {
		providerID = newUUID(t)
	}
	_, err := store.pool.Exec(context.Background(), `
INSERT INTO payments (id,order_id,provider,provider_payment_id,provider_idempotency_key,status,amount_minor,currency,confirmation_url,provider_create_deadline,next_reconcile_at)
VALUES ($1,$2,'yookassa',$3,$4,$5,29900,'RUB',$6,$7,now()-interval '1 minute')`, paymentID, orderID, providerID, newUUID(t), status, confirmationURL, deadline)
	if err != nil {
		t.Fatal(err)
	}
	return paymentID
}

func verifiedProviderPayment(operation domain.PaymentOperation, status string, occurredAt time.Time) domain.ProviderPayment {
	payment := domain.ProviderPayment{
		ProviderPaymentID: *operation.ProviderPaymentID,
		Status:            status,
		AmountMinor:       operation.AmountMinor,
		Currency:          operation.Currency,
		Test:              true,
		AccountID:         "test-shop",
		MetadataOrderID:   operation.OrderID,
		MetadataPaymentID: operation.PaymentID,
	}
	if status == "succeeded" {
		payment.CapturedAt = &occurredAt
	} else {
		payment.CanceledAt = &occurredAt
	}
	return payment
}

func assertCount(t *testing.T, store *Store, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := store.pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count=%d, want %d", got, want)
	}
}

func assertPostgresCode(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("error=%v, want PostgreSQL code %s", err, code)
	}
}

func newUUID(t *testing.T) string {
	t.Helper()
	id, err := cryptoutil.RandomUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
