package postgres

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

func TestIntegrationPaymentIsIdempotentAndConcurrentExtensionsSerialize(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	first, firstOrder := paymentFixture(t, userID, now)
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, first), first, firstOrder, now); err != nil {
		t.Fatal(err)
	}
	duplicateMeta := paymentMeta(t, first)
	if err := store.ApplyPayment(context.Background(), duplicateMeta, first, firstOrder, now); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM subscription_periods`, 1)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.activated.v1'`, 1)

	second, secondOrder := paymentFixture(t, userID, now.Add(time.Hour))
	third, thirdOrder := paymentFixture(t, userID, now.Add(2*time.Hour))
	secondMeta := paymentMeta(t, second)
	thirdMeta := paymentMeta(t, third)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, input := range []struct {
		payment domain.PaymentSucceeded
		order   domain.Order
		meta    domain.EventMeta
	}{{second, secondOrder, secondMeta}, {third, thirdOrder, thirdMeta}} {
		wg.Add(1)
		go func(payment domain.PaymentSucceeded, order domain.Order, meta domain.EventMeta) {
			defer wg.Done()
			<-start
			errs <- store.ApplyPayment(context.Background(), meta, payment, order, now.Add(3*time.Hour))
		}(input.payment, input.order, input.meta)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, store, `SELECT count(*) FROM subscription_periods`, 3)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.extended.v1'`, 2)
	var periodEnd time.Time
	if err := store.pool.QueryRow(context.Background(), `SELECT current_period_end FROM subscriptions WHERE user_id=$1`, userID).Scan(&periodEnd); err != nil {
		t.Fatal(err)
	}
	wantEnd := now.Add(90 * 24 * time.Hour)
	if !periodEnd.Equal(wantEnd) {
		t.Fatalf("period end=%s, want %s", periodEnd, wantEnd)
	}
}

func TestIntegrationLifecycleExactBoundariesAndLeaseRecovery(t *testing.T) {
	store := openIntegrationStore(t)
	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, start)
	order.PlanSnapshot.DurationDays = 1
	order.PlanSnapshot.GracePeriodHours = 1
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, payment), payment, order, start); err != nil {
		t.Fatal(err)
	}
	periodEnd := start.Add(24 * time.Hour)
	claimed, ok, err := store.ClaimDue(context.Background(), periodEnd, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim at period boundary ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.ClaimDue(context.Background(), periodEnd.Add(30*time.Second), time.Minute); err != nil || ok {
		t.Fatalf("lease was not respected ok=%v err=%v", ok, err)
	}
	claimed, ok, err = store.ClaimDue(context.Background(), periodEnd.Add(time.Minute), time.Minute)
	if err != nil || !ok {
		t.Fatalf("expired lease was not recovered ok=%v err=%v", ok, err)
	}
	if err := store.CompleteDue(context.Background(), claimed.SubscriptionID, periodEnd); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusGrace)
	if _, ok, err := store.ClaimDue(context.Background(), periodEnd.Add(time.Hour-time.Nanosecond), time.Minute); err != nil || ok {
		t.Fatalf("claimed before grace boundary ok=%v err=%v", ok, err)
	}
	claimed, ok, err = store.ClaimDue(context.Background(), periodEnd.Add(time.Hour), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim at grace boundary ok=%v err=%v", ok, err)
	}
	if err := store.CompleteDue(context.Background(), claimed.SubscriptionID, periodEnd.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusExpired)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.expired.v1'`, 1)
	var effectiveAt time.Time
	if err := store.pool.QueryRow(context.Background(), `SELECT (payload->'data'->>'effective_at')::timestamptz FROM outbox WHERE topic='subscription.expired.v1'`).Scan(&effectiveAt); err != nil {
		t.Fatal(err)
	}
	if !effectiveAt.Equal(periodEnd.Add(time.Hour)) {
		t.Fatalf("expiry effective_at=%s, want grace boundary %s", effectiveAt, periodEnd.Add(time.Hour))
	}
}

func TestIntegrationRefundBeforePaymentReconcilesAndRevokes(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, now)
	refund := refundFixture(t, payment, now.Add(time.Hour))
	if err := store.StoreRefund(context.Background(), refundMeta(t, refund), refund, now); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM inbox WHERE state='pending'`, 1)
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, payment), payment, order, now); err != nil {
		t.Fatal(err)
	}
	work, ok, err := store.ClaimRefund(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim pending refund ok=%v err=%v", ok, err)
	}
	if err := store.ApplyClaimedRefund(context.Background(), work, now.Add(time.Hour), time.Second); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusRevoked)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 1)
}

func TestIntegrationDelayedPaymentEmitsActivationThenBoundaryExpiry(t *testing.T) {
	store := openIntegrationStore(t)
	paidAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	now := paidAt.Add(48 * time.Hour)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, paidAt)
	order.PlanSnapshot.DurationDays = 1
	order.PlanSnapshot.GracePeriodHours = 1
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, payment), payment, order, now); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusActive)
	claimed, ok, err := store.ClaimDue(context.Background(), now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("delayed payment was not immediately due: ok=%v err=%v", ok, err)
	}
	if err := store.CompleteDue(context.Background(), claimed.SubscriptionID, now); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusExpired)
	var topics []string
	rows, err := store.pool.Query(context.Background(), `SELECT topic FROM outbox ORDER BY created_at, topic`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			t.Fatal(err)
		}
		topics = append(topics, topic)
	}
	if len(topics) != 2 || topics[0] != "subscription.activated.v1" || topics[1] != "subscription.expired.v1" {
		t.Fatalf("topics=%v", topics)
	}
	var effectiveAt time.Time
	if err := store.pool.QueryRow(context.Background(), `SELECT (payload->'data'->>'effective_at')::timestamptz FROM outbox WHERE topic='subscription.expired.v1'`).Scan(&effectiveAt); err != nil {
		t.Fatal(err)
	}
	wantBoundary := paidAt.Add(25 * time.Hour)
	if !effectiveAt.Equal(wantBoundary) {
		t.Fatalf("effective_at=%s, want %s", effectiveAt, wantBoundary)
	}
}

func TestIntegrationRefundCurrentFutureAndHistoricalPeriods(t *testing.T) {
	t.Run("current with future remains pending", func(t *testing.T) {
		store := openIntegrationStore(t)
		now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
		userID := newUUID(t)
		first, firstOrder := paymentFixture(t, userID, now)
		second, secondOrder := paymentFixture(t, userID, now.Add(time.Hour))
		applyPayments(t, store, now, first, firstOrder, second, secondOrder)
		refund := refundFixture(t, first, now.Add(2*time.Hour))
		if err := store.StoreRefund(context.Background(), refundMeta(t, refund), refund, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, store, userID, domain.StatusPending)
		assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 0)
	})

	t.Run("future refund preserves current", func(t *testing.T) {
		store := openIntegrationStore(t)
		now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
		userID := newUUID(t)
		first, firstOrder := paymentFixture(t, userID, now)
		second, secondOrder := paymentFixture(t, userID, now.Add(time.Hour))
		applyPayments(t, store, now, first, firstOrder, second, secondOrder)
		refund := refundFixture(t, second, now.Add(2*time.Hour))
		if err := store.StoreRefund(context.Background(), refundMeta(t, refund), refund, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, store, userID, domain.StatusActive)
		assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 0)
	})

	t.Run("historical refund preserves current", func(t *testing.T) {
		store := openIntegrationStore(t)
		start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
		userID := newUUID(t)
		first, firstOrder := paymentFixture(t, userID, start)
		firstOrder.PlanSnapshot.DurationDays = 1
		firstOrder.PlanSnapshot.GracePeriodHours = 0
		second, secondOrder := paymentFixture(t, userID, start.Add(time.Hour))
		secondOrder.PlanSnapshot.DurationDays = 2
		secondOrder.PlanSnapshot.GracePeriodHours = 0
		applyPayments(t, store, start, first, firstOrder, second, secondOrder)
		now := start.Add(25 * time.Hour)
		refund := refundFixture(t, first, now)
		if err := store.StoreRefund(context.Background(), refundMeta(t, refund), refund, now); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, store, userID, domain.StatusActive)
		assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 0)
	})
}

func TestIntegrationPeriodSnapshotAndOutboxAreAtomic(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, now)
	if _, err := store.pool.Exec(context.Background(), `ALTER TABLE outbox ADD CONSTRAINT reject_test_outbox CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `ALTER TABLE outbox DROP CONSTRAINT IF EXISTS reject_test_outbox`)
	})
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, payment), payment, order, now); err == nil {
		t.Fatal("payment application succeeded while outbox insert was rejected")
	}
	if _, err := store.pool.Exec(context.Background(), `ALTER TABLE outbox DROP CONSTRAINT reject_test_outbox`); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM subscriptions`, 0)
	assertCount(t, store, `SELECT count(*) FROM subscription_periods`, 0)
	assertCount(t, store, `SELECT count(*) FROM inbox`, 0)
}

func openIntegrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("SUBSCRIPTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SUBSCRIPTION_TEST_DATABASE_URL is not set")
	}
	store, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	resetSubscription(t, store)
	t.Cleanup(func() {
		resetSubscription(t, store)
		store.Close()
	})
	return store
}

func resetSubscription(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE outbox,consumer_dead_letters,inbox,subscription_periods,subscriptions`); err != nil {
		t.Fatal(err)
	}
}

func paymentFixture(t *testing.T, userID string, paidAt time.Time) (domain.PaymentSucceeded, domain.Order) {
	t.Helper()
	payment := domain.PaymentSucceeded{PaymentID: newUUID(t), OrderID: newUUID(t), UserID: userID, PlanID: "vpn-30d-v1", AmountMinor: 29900, Currency: "RUB", PaidAt: paidAt}
	order := domain.Order{OrderID: payment.OrderID, UserID: userID, Status: "paid", AmountMinor: payment.AmountMinor, Currency: payment.Currency, PlanSnapshot: domain.PlanSnapshot{PlanID: payment.PlanID, DurationDays: 30, GracePeriodHours: 24, AmountMinor: payment.AmountMinor, Currency: payment.Currency, Region: "ru-test"}}
	return payment, order
}

func paymentMeta(t *testing.T, payment domain.PaymentSucceeded) domain.EventMeta {
	t.Helper()
	return domain.EventMeta{EventID: newUUID(t), EventType: "billing.payment.succeeded.v1", AggregateID: payment.PaymentID, CorrelationID: newUUID(t), OccurredAt: payment.PaidAt}
}

func refundFixture(t *testing.T, payment domain.PaymentSucceeded, refundedAt time.Time) domain.RefundSucceeded {
	t.Helper()
	return domain.RefundSucceeded{RefundID: newUUID(t), PaymentID: payment.PaymentID, OrderID: payment.OrderID, UserID: payment.UserID, AmountMinor: payment.AmountMinor, Currency: payment.Currency, RefundScope: "full", RefundedAt: refundedAt}
}

func refundMeta(t *testing.T, refund domain.RefundSucceeded) domain.EventMeta {
	t.Helper()
	return domain.EventMeta{EventID: newUUID(t), EventType: "billing.refund.succeeded.v1", AggregateID: refund.RefundID, CorrelationID: newUUID(t), OccurredAt: refund.RefundedAt}
}

func applyPayments(t *testing.T, store *Store, now time.Time, first domain.PaymentSucceeded, firstOrder domain.Order, second domain.PaymentSucceeded, secondOrder domain.Order) {
	t.Helper()
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, first), first, firstOrder, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPayment(context.Background(), paymentMeta(t, second), second, secondOrder, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func assertStatus(t *testing.T, store *Store, userID, want string) {
	t.Helper()
	var got string
	if err := store.pool.QueryRow(context.Background(), `SELECT status FROM subscriptions WHERE user_id=$1`, userID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("subscription status=%s, want %s", got, want)
	}
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

func newUUID(t *testing.T) string {
	t.Helper()
	id, err := cryptoutil.RandomUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
