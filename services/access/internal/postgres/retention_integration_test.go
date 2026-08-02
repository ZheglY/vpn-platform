package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestIntegrationRetentionDeletesOnlyExpiredSecurityAuditInBoundedBatch(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "72000000-0000-4000-8000-000000000001"
	credentialID := "72000000-0000-4000-8000-000000000002"
	if err := store.ApplyPeriod(ctx,
		integrationMeta("72000000-0000-4000-8000-000000000003", "subscription.activated.v1", subscriptionID, 1, 1),
		domain.PeriodEvent{
			SubscriptionID: subscriptionID, UserID: "72000000-0000-4000-8000-000000000004",
			PeriodID: "72000000-0000-4000-8000-000000000005", SourceOrderID: "72000000-0000-4000-8000-000000000006",
			SourcePaymentID: "72000000-0000-4000-8000-000000000007", PeriodStart: now,
			PeriodEnd: now.Add(30 * 24 * time.Hour), GraceEndsAt: now.Add(31 * 24 * time.Hour),
		},
		domain.CredentialSeed{CredentialID: credentialID, OperationID: "72000000-0000-4000-8000-000000000008", Ciphertext: bytes.Repeat([]byte{1}, 64), KeyVersion: 1},
	); err != nil {
		t.Fatal(err)
	}
	_, err := store.pool.Exec(ctx, `
INSERT INTO security_audit_events (event_id,action,outcome,actor_service,credential_id,occurred_at) VALUES
('72000000-0000-4000-8000-000000000011','credential_material.read','succeeded','provisioning-service',$1,$2),
('72000000-0000-4000-8000-000000000012','credential_material.read','succeeded','provisioning-service',$1,$2),
('72000000-0000-4000-8000-000000000013','credential_material.read','succeeded','provisioning-service',$1,$3)`,
		credentialID, now.Add(-400*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	dataset := store.RetentionDatasets(365 * 24 * time.Hour)[0]
	if deleted, err := dataset.DeleteBatch(ctx, now.Add(-365*24*time.Hour), 1); err != nil || deleted != 1 {
		t.Fatalf("bounded delete = %d, %v", deleted, err)
	}
	var remaining int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM security_audit_events`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("remaining security audit events = %d, want 2", remaining)
	}
}
