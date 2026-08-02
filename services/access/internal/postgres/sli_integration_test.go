package postgres

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestIntegrationDurablePaymentAccessSLITracksOverdueAndReplay(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	payment := domain.PaymentSucceeded{
		PaymentID:   "73000000-0000-4000-8000-000000000001",
		OrderID:     "73000000-0000-4000-8000-000000000002",
		UserID:      "73000000-0000-4000-8000-000000000003",
		PlanID:      "73000000-0000-4000-8000-000000000004",
		AmountMinor: 49900,
		Currency:    "RUB",
		PaidAt:      now.Add(-61 * time.Second),
	}
	meta := integrationMeta(
		"73000000-0000-4000-8000-000000000005",
		"billing.payment.succeeded.v1",
		payment.PaymentID,
		1,
		2,
	)
	if err := store.ApplyPaymentSucceeded(ctx, meta, payment); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPaymentSucceeded(ctx, meta, payment); err != nil {
		t.Fatalf("payment replay: %v", err)
	}
	snapshot, err := store.PaymentAccessSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Started != 1 || snapshot.Bad != 1 {
		t.Fatalf("overdue snapshot = %#v, want started=1 bad=1", snapshot)
	}

	reopened, err := Open(ctx, os.Getenv("ACCESS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restartedSnapshot, err := reopened.PaymentAccessSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restartedSnapshot != snapshot {
		t.Fatalf("restart snapshot = %#v, want %#v", restartedSnapshot, snapshot)
	}
}

func TestIntegrationDurablePaymentAccessSLIReconcilesOutOfOrderCompletion(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	paymentID := "74000000-0000-4000-8000-000000000001"
	userID := "74000000-0000-4000-8000-000000000002"
	subscriptionID := "74000000-0000-4000-8000-000000000003"
	period := domain.PeriodEvent{
		SubscriptionID:  subscriptionID,
		UserID:          userID,
		PeriodID:        "74000000-0000-4000-8000-000000000004",
		SourceOrderID:   "74000000-0000-4000-8000-000000000005",
		SourcePaymentID: paymentID,
		PeriodStart:     now.Add(-5 * time.Second),
		PeriodEnd:       now.Add(30 * 24 * time.Hour),
		GraceEndsAt:     now.Add(31 * 24 * time.Hour),
	}
	seed := domain.CredentialSeed{
		CredentialID: "74000000-0000-4000-8000-000000000006",
		OperationID:  "74000000-0000-4000-8000-000000000007",
		Ciphertext:   bytes.Repeat([]byte{0x74}, 64),
		KeyVersion:   1,
	}
	if err := store.ApplyPeriod(ctx, integrationMeta(
		"74000000-0000-4000-8000-000000000008",
		"subscription.activated.v1",
		subscriptionID,
		1,
		1,
	), period, seed); err != nil {
		t.Fatal(err)
	}
	payment := domain.PaymentSucceeded{
		PaymentID: paymentID, OrderID: period.SourceOrderID, UserID: userID,
		PlanID:      "74000000-0000-4000-8000-000000000009",
		AmountMinor: 49900, Currency: "RUB", PaidAt: now.Add(-10 * time.Second),
	}
	if err := store.ApplyPaymentSucceeded(ctx, integrationMeta(
		"74000000-0000-4000-8000-000000000010",
		"billing.payment.succeeded.v1",
		paymentID,
		2,
		2,
	), payment); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.PaymentAccessSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Started != 1 || snapshot.Bad != 0 {
		t.Fatalf("out-of-order snapshot = %#v, want started=1 bad=0", snapshot)
	}
	var paymentEventID, fulfillmentEventID string
	if err := store.pool.QueryRow(ctx, `
SELECT payment_event_id, fulfillment_event_id
FROM payment_access_sli WHERE payment_id=$1`, paymentID).Scan(&paymentEventID, &fulfillmentEventID); err != nil {
		t.Fatal(err)
	}
	if paymentEventID == "" || fulfillmentEventID == "" {
		t.Fatal("durable projection did not retain both causal events")
	}
}

func TestIntegrationDurablePaymentAccessSLIRollsBackWithProvisioningCommand(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	period := domain.PeriodEvent{
		SubscriptionID:  "75000000-0000-4000-8000-000000000001",
		UserID:          "75000000-0000-4000-8000-000000000002",
		PeriodID:        "75000000-0000-4000-8000-000000000003",
		SourceOrderID:   "75000000-0000-4000-8000-000000000004",
		SourcePaymentID: "75000000-0000-4000-8000-000000000005",
		PeriodStart:     now,
		PeriodEnd:       now.Add(30 * 24 * time.Hour),
		GraceEndsAt:     now.Add(31 * 24 * time.Hour),
	}
	seed := domain.CredentialSeed{
		CredentialID: "75000000-0000-4000-8000-000000000006",
		OperationID:  "75000000-0000-4000-8000-000000000007",
		Ciphertext:   bytes.Repeat([]byte{0x75}, 64),
		KeyVersion:   1,
	}
	if _, err := store.pool.Exec(ctx, `ALTER TABLE outbox ADD CONSTRAINT force_sli_rollback CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `ALTER TABLE outbox DROP CONSTRAINT IF EXISTS force_sli_rollback`)
	})
	if err := store.ApplyPeriod(ctx, integrationMeta(
		"75000000-0000-4000-8000-000000000008",
		"subscription.activated.v1",
		period.SubscriptionID,
		1,
		1,
	), period, seed); err == nil {
		t.Fatal("period unexpectedly committed")
	}
	assertCount(t, store, "payment_access_sli", 0)
}
