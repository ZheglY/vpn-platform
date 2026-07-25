package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

type paymentProvisioningRecorder struct {
	observations []time.Duration
}

func (r *paymentProvisioningRecorder) ObservePaymentToProvisioning(elapsed time.Duration) {
	r.observations = append(r.observations, elapsed)
}

func TestIntegrationPaymentProvisioningSLIRecordsOnlyCommittedInitialActivation(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	recorder := &paymentProvisioningRecorder{}
	store.SetPaymentProvisioningObserver(recorder)

	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "73000000-0000-4000-8000-000000000001"
	userID := "73000000-0000-4000-8000-000000000002"
	period := domain.PeriodEvent{
		SubscriptionID:  subscriptionID,
		UserID:          userID,
		PeriodID:        "73000000-0000-4000-8000-000000000003",
		SourceOrderID:   "73000000-0000-4000-8000-000000000004",
		SourcePaymentID: "73000000-0000-4000-8000-000000000005",
		PeriodStart:     now.Add(-5 * time.Second),
		PeriodEnd:       now.Add(30 * 24 * time.Hour),
		GraceEndsAt:     now.Add(31 * 24 * time.Hour),
	}
	seed := domain.CredentialSeed{
		CredentialID: "73000000-0000-4000-8000-000000000006",
		OperationID:  "73000000-0000-4000-8000-000000000007",
		Ciphertext:   bytes.Repeat([]byte{0x73}, 64),
		KeyVersion:   1,
	}
	activation := integrationMeta(
		"73000000-0000-4000-8000-000000000008",
		"subscription.activated.v1",
		subscriptionID,
		1,
		1,
	)
	if err := store.ApplyPeriod(ctx, activation, period, seed); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPeriod(ctx, activation, period, seed); err != nil {
		t.Fatalf("activation replay: %v", err)
	}

	extension := period
	extension.PeriodID = "73000000-0000-4000-8000-000000000009"
	extension.PeriodEnd = period.PeriodEnd.Add(30 * 24 * time.Hour)
	extension.GraceEndsAt = period.GraceEndsAt.Add(30 * 24 * time.Hour)
	if err := store.ApplyPeriod(ctx, integrationMeta(
		"73000000-0000-4000-8000-000000000010",
		"subscription.extended.v1",
		subscriptionID,
		2,
		2,
	), extension, seed); err != nil {
		t.Fatalf("extension: %v", err)
	}

	rollbackPeriod := period
	rollbackPeriod.SubscriptionID = "73000000-0000-4000-8000-000000000011"
	rollbackPeriod.UserID = "73000000-0000-4000-8000-000000000012"
	rollbackPeriod.PeriodID = "73000000-0000-4000-8000-000000000013"
	rollbackSeed := seed
	rollbackSeed.OperationID = "73000000-0000-4000-8000-000000000014"
	if err := store.ApplyPeriod(ctx, integrationMeta(
		"73000000-0000-4000-8000-000000000015",
		"subscription.activated.v1",
		rollbackPeriod.SubscriptionID,
		3,
		1,
	), rollbackPeriod, rollbackSeed); err == nil {
		t.Fatal("activation with a duplicate credential ID unexpectedly committed")
	}

	if len(recorder.observations) != 1 {
		t.Fatalf("payment-to-provisioning observations = %d, want 1", len(recorder.observations))
	}
	if recorder.observations[0] < 5*time.Second || recorder.observations[0] > 10*time.Second {
		t.Fatalf("payment-to-provisioning observation = %v, want approximately 5s", recorder.observations[0])
	}
}
