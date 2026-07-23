package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

const zeroPayloadHash = "0000000000000000000000000000000000000000000000000000000000000000"

var sourceOffsets atomic.Int64

func TestIntegrationPaymentIsIdempotentAndConcurrentExtensionsSerialize(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	first, firstOrder := paymentFixture(t, userID, now)
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, first), first, firstOrder, now); err != nil {
		t.Fatal(err)
	}
	duplicateMeta := paymentMeta(t, first)
	if err := store.applyPaymentAt(context.Background(), duplicateMeta, first, firstOrder, now); err != nil {
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
			errs <- store.applyPaymentAt(context.Background(), meta, payment, order, now.Add(3*time.Hour))
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
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, start); err != nil {
		t.Fatal(err)
	}
	periodEnd := start.Add(24 * time.Hour)
	claimed, ok, err := store.claimDueAt(context.Background(), periodEnd, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim at period boundary ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.claimDueAt(context.Background(), periodEnd.Add(30*time.Second), time.Minute); err != nil || ok {
		t.Fatalf("lease was not respected ok=%v err=%v", ok, err)
	}
	claimed, ok, err = store.claimDueAt(context.Background(), periodEnd.Add(time.Minute), time.Minute)
	if err != nil || !ok {
		t.Fatalf("expired lease was not recovered ok=%v err=%v", ok, err)
	}
	if err := store.completeDueAt(context.Background(), claimed.SubscriptionID, periodEnd); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusGrace)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.grace.started.v1'`, 1)
	if _, ok, err := store.claimDueAt(context.Background(), periodEnd.Add(time.Hour-time.Nanosecond), time.Minute); err != nil || ok {
		t.Fatalf("claimed before grace boundary ok=%v err=%v", ok, err)
	}
	claimed, ok, err = store.claimDueAt(context.Background(), periodEnd.Add(time.Hour), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim at grace boundary ok=%v err=%v", ok, err)
	}
	if err := store.completeDueAt(context.Background(), claimed.SubscriptionID, periodEnd.Add(time.Hour)); err != nil {
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

func TestIntegrationAdminRevokeIsOwnerControlledAndIdempotent(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, now)
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, now); err != nil {
		t.Fatal(err)
	}
	var subscriptionID string
	if err := store.pool.QueryRow(context.Background(), `SELECT id FROM subscriptions WHERE user_id=$1`, userID).Scan(&subscriptionID); err != nil {
		t.Fatal(err)
	}
	input := domain.AdminRevokeInput{
		SubscriptionID: subscriptionID, IdempotencyKey: "admin-revoke-integration-0001", RequestSHA256: strings.Repeat("a", 64),
		ActionID: newUUID(t), CorrelationID: newUUID(t), ReasonCode: "abuse",
	}
	result, err := store.AdminRevoke(context.Background(), input)
	if err != nil || result.Replay || result.Status != domain.StatusRevoked || result.ReasonCode != "abuse" {
		t.Fatalf("admin revoke=%+v err=%v", result, err)
	}
	replay, err := store.AdminRevoke(context.Background(), input)
	if err != nil || !replay.Replay {
		t.Fatalf("admin revoke replay=%+v err=%v", replay, err)
	}
	collision := input
	collision.RequestSHA256 = strings.Repeat("b", 64)
	if _, err := store.AdminRevoke(context.Background(), collision); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("admin revoke collision=%v", err)
	}
	assertCount(t, store, `SELECT count(*) FROM admin_revoke_requests`, 1)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 1)
}

func TestIntegrationSchedulerProductionPathUsesPostgresTime(t *testing.T) {
	store := openIntegrationStore(t)
	paidAt := time.Now().UTC().Add(-48 * time.Hour)
	payment, order := paymentFixture(t, newUUID(t), paidAt)
	order.PlanSnapshot.DurationDays = 1
	order.PlanSnapshot.GracePeriodHours = 1
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, paidAt); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimDue(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("production DB-time claim ok=%v err=%v", ok, err)
	}
	if err := store.CompleteDue(context.Background(), claimed.SubscriptionID); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, payment.UserID, domain.StatusExpired)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.expired.v1'`, 1)
}

func TestIntegrationRefundBeforePaymentReconcilesAndRevokes(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, now)
	refund := refundFixture(t, payment, now.Add(time.Hour))
	if err := store.storeRefundAt(context.Background(), refundMeta(t, refund), refund, now); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM inbox WHERE state='pending'`, 1)
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, now); err != nil {
		t.Fatal(err)
	}
	work, ok, err := store.ClaimRefund(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim pending refund ok=%v err=%v", ok, err)
	}
	if err := store.applyClaimedRefundAt(context.Background(), work, now.Add(time.Hour), time.Second); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, store, userID, domain.StatusRevoked)
	assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1'`, 1)
}

func TestIntegrationConflictingRefundBeforePaymentMovesAtomicallyToDeadLetter(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	originalUserID := newUUID(t)
	payment, _ := paymentFixture(t, originalUserID, now)
	refund := refundFixture(t, payment, now.Add(time.Hour))
	if err := store.storeRefundAt(context.Background(), refundMeta(t, refund), refund, now); err != nil {
		t.Fatal(err)
	}

	conflictingPayment := payment
	conflictingPayment.UserID = newUUID(t)
	conflictingPayment.OrderID = newUUID(t)
	conflictingOrder := domain.Order{
		OrderID: conflictingPayment.OrderID, UserID: conflictingPayment.UserID, Status: "paid",
		AmountMinor: conflictingPayment.AmountMinor, Currency: conflictingPayment.Currency,
		PlanSnapshot: domain.PlanSnapshot{PlanID: conflictingPayment.PlanID, DurationDays: 30, GracePeriodHours: 24, AmountMinor: conflictingPayment.AmountMinor, Currency: conflictingPayment.Currency, Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1},
	}
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, conflictingPayment), conflictingPayment, conflictingOrder, now); err != nil {
		t.Fatal(err)
	}
	work, ok, err := store.ClaimRefund(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim conflicting refund ok=%v err=%v", ok, err)
	}
	if err := store.applyClaimedRefundAt(context.Background(), work, now.Add(time.Hour), time.Second); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM inbox WHERE event_id=$1 AND state='dead' AND payload IS NULL AND last_error_code='durable_state_conflict'`, 1, work.InboxID)
	assertCount(t, store, `SELECT count(*) FROM consumer_dead_letters WHERE topic=$1 AND partition=$2 AND record_offset=$3 AND payload_sha256=$4 AND reason_code='durable_state_conflict'`, 1, work.Meta.SourceTopic, work.Meta.SourcePartition, work.Meta.SourceOffset, work.Meta.PayloadSHA256)
	if _, ok, err := store.ClaimRefund(context.Background(), time.Minute); err != nil || ok {
		t.Fatalf("dead refund was claimed again ok=%v err=%v", ok, err)
	}
}

func TestIntegrationDelayedPaymentCommitsFinalExpiredState(t *testing.T) {
	store := openIntegrationStore(t)
	paidAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	now := paidAt.Add(48 * time.Hour)
	userID := newUUID(t)
	payment, order := paymentFixture(t, userID, paidAt)
	order.PlanSnapshot.DurationDays = 1
	order.PlanSnapshot.GracePeriodHours = 1
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, now); err != nil {
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
	if len(topics) != 1 || topics[0] != "subscription.expired.v1" {
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
		if err := store.storeRefundAt(context.Background(), refundMeta(t, refund), refund, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, store, userID, domain.StatusPending)
		assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.revoked.v1' AND payload->'data'->>'reason'='refund_gap'`, 1)
		futureStart := now.Add(30 * 24 * time.Hour)
		claimed, ok, err := store.claimDueAt(context.Background(), futureStart, time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim future period ok=%v err=%v", ok, err)
		}
		if err := store.completeDueAt(context.Background(), claimed.SubscriptionID, futureStart); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, store, userID, domain.StatusActive)
		assertCount(t, store, `SELECT count(*) FROM outbox WHERE topic='subscription.activated.v1'`, 2)
	})

	t.Run("future refund preserves current", func(t *testing.T) {
		store := openIntegrationStore(t)
		now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
		userID := newUUID(t)
		first, firstOrder := paymentFixture(t, userID, now)
		second, secondOrder := paymentFixture(t, userID, now.Add(time.Hour))
		applyPayments(t, store, now, first, firstOrder, second, secondOrder)
		refund := refundFixture(t, second, now.Add(2*time.Hour))
		if err := store.storeRefundAt(context.Background(), refundMeta(t, refund), refund, now.Add(2*time.Hour)); err != nil {
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
		if err := store.storeRefundAt(context.Background(), refundMeta(t, refund), refund, now); err != nil {
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
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, payment), payment, order, now); err == nil {
		t.Fatal("payment application succeeded while outbox insert was rejected")
	}
	if _, err := store.pool.Exec(context.Background(), `ALTER TABLE outbox DROP CONSTRAINT reject_test_outbox`); err != nil {
		t.Fatal(err)
	}
	assertCount(t, store, `SELECT count(*) FROM subscriptions`, 0)
	assertCount(t, store, `SELECT count(*) FROM subscription_periods`, 0)
	assertCount(t, store, `SELECT count(*) FROM inbox`, 0)
}

func TestIntegrationPaymentReplayRejectsChangedTermsAndEventPayload(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	payment, order := paymentFixture(t, newUUID(t), now)
	meta := paymentMeta(t, payment)
	if err := store.applyPaymentAt(context.Background(), meta, payment, order, now); err != nil {
		t.Fatal(err)
	}

	changedTerms := payment
	changedTerms.AmountMinor++
	if _, err := store.recordPaymentReplayAt(context.Background(), paymentMeta(t, changedTerms), changedTerms, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed payment terms error=%v, want durable conflict", err)
	}

	changedPayloadMeta := meta
	changedPayloadMeta.PayloadSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := store.applyPaymentAt(context.Background(), changedPayloadMeta, payment, order, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed event payload error=%v, want durable conflict", err)
	}
	assertCount(t, store, `SELECT count(*) FROM subscription_periods`, 1)
	assertCount(t, store, `SELECT count(*) FROM inbox`, 1)
}

func TestIntegrationOutboxPreventsReverseCompletionAcrossWorkers(t *testing.T) {
	store := openIntegrationStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	userID := newUUID(t)
	first, firstOrder := paymentFixture(t, userID, now)
	second, secondOrder := paymentFixture(t, userID, now.Add(time.Hour))
	applyPayments(t, store, now, first, firstOrder, second, secondOrder)

	firstMessage, ok, err := store.ClaimOutbox(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("first worker claim ok=%v err=%v", ok, err)
	}
	type claimResult struct {
		message domain.OutboxMessage
		ok      bool
		err     error
	}
	reverseAttempt := make(chan claimResult, 1)
	go func() {
		message, claimed, claimErr := store.ClaimOutbox(context.Background(), time.Minute)
		reverseAttempt <- claimResult{message: message, ok: claimed, err: claimErr}
	}()
	result := <-reverseAttempt
	if result.err != nil || result.ok {
		t.Fatalf("second worker bypassed unpublished sequence: ok=%v event=%s err=%v", result.ok, result.message.EventID, result.err)
	}
	if err := store.CompleteOutbox(context.Background(), firstMessage.EventID); err != nil {
		t.Fatal(err)
	}
	secondMessage, ok, err := store.ClaimOutbox(context.Background(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("second worker claim after first completion ok=%v err=%v", ok, err)
	}
	var firstSequence, secondSequence int64
	if err := store.pool.QueryRow(context.Background(), `SELECT aggregate_sequence FROM outbox WHERE event_id=$1`, firstMessage.EventID).Scan(&firstSequence); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(context.Background(), `SELECT aggregate_sequence FROM outbox WHERE event_id=$1`, secondMessage.EventID).Scan(&secondSequence); err != nil {
		t.Fatal(err)
	}
	if firstSequence != 1 || secondSequence != 2 {
		t.Fatalf("delivery sequences=%d,%d, want 1,2", firstSequence, secondSequence)
	}
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
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE admin_revoke_requests,outbox,consumer_dead_letters,inbox,subscription_periods,subscriptions`); err != nil {
		t.Fatal(err)
	}
}

func paymentFixture(t *testing.T, userID string, paidAt time.Time) (domain.PaymentSucceeded, domain.Order) {
	t.Helper()
	payment := domain.PaymentSucceeded{PaymentID: newUUID(t), OrderID: newUUID(t), UserID: userID, PlanID: "vpn-30d-v1", AmountMinor: 29900, Currency: "RUB", PaidAt: paidAt}
	order := domain.Order{OrderID: payment.OrderID, UserID: userID, Status: "paid", AmountMinor: payment.AmountMinor, Currency: payment.Currency, PlanSnapshot: domain.PlanSnapshot{PlanID: payment.PlanID, DurationDays: 30, GracePeriodHours: 24, AmountMinor: payment.AmountMinor, Currency: payment.Currency, Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1}}
	return payment, order
}

func paymentMeta(t *testing.T, payment domain.PaymentSucceeded) domain.EventMeta {
	t.Helper()
	return domain.EventMeta{EventID: newUUID(t), EventType: "billing.payment.succeeded.v1", AggregateID: payment.PaymentID, CorrelationID: newUUID(t), OccurredAt: payment.PaidAt, SourceTopic: "billing.payment.succeeded.v1", SourcePartition: 0, SourceOffset: sourceOffsets.Add(1), PayloadSHA256: zeroPayloadHash}
}

func refundFixture(t *testing.T, payment domain.PaymentSucceeded, refundedAt time.Time) domain.RefundSucceeded {
	t.Helper()
	return domain.RefundSucceeded{RefundID: newUUID(t), PaymentID: payment.PaymentID, OrderID: payment.OrderID, UserID: payment.UserID, AmountMinor: payment.AmountMinor, Currency: payment.Currency, RefundScope: "full", RefundedAt: refundedAt}
}

func refundMeta(t *testing.T, refund domain.RefundSucceeded) domain.EventMeta {
	t.Helper()
	return domain.EventMeta{EventID: newUUID(t), EventType: "billing.refund.succeeded.v1", AggregateID: refund.RefundID, CorrelationID: newUUID(t), OccurredAt: refund.RefundedAt, SourceTopic: "billing.refund.succeeded.v1", SourcePartition: 0, SourceOffset: sourceOffsets.Add(1), PayloadSHA256: zeroPayloadHash}
}

func applyPayments(t *testing.T, store *Store, now time.Time, first domain.PaymentSucceeded, firstOrder domain.Order, second domain.PaymentSucceeded, secondOrder domain.Order) {
	t.Helper()
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, first), first, firstOrder, now); err != nil {
		t.Fatal(err)
	}
	if err := store.applyPaymentAt(context.Background(), paymentMeta(t, second), second, secondOrder, now.Add(time.Hour)); err != nil {
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
