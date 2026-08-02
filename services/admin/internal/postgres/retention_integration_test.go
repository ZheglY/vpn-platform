package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIntegrationAdminAuditRetentionRequiresMigratorAndCutoff(t *testing.T) {
	runtimeStore, cleanup := adminIntegrationStores(t)
	defer cleanup()
	ctx := context.Background()
	migratorStore, err := Open(ctx, os.Getenv("ADMIN_MIGRATOR_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer migratorStore.Close()
	now := time.Now().UTC()
	principal := "spiffe://vpn-service/ns/local/admin/integration-ops"
	_, err = migratorStore.pool.Exec(ctx, `
INSERT INTO admin_action_requests (
    action_id,actor_spiffe_id,actor_principal,action,permission,target_type,target_id,reason,
    idempotency_key_sha256,request_sha256,request_id,correlation_id,roles_snapshot,permissions_snapshot,status
) VALUES (
    '73000000-0000-4000-8000-000000000001',$1,'integration-ops','notification.retry','notification.retry',
    'notification','73000000-0000-4000-8000-000000000002','retention integration',
    $2,$3,'retention-request','73000000-0000-4000-8000-000000000003',ARRAY['operations'],ARRAY['notification.retry'],'pending'
)`,
		principal, strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = migratorStore.pool.Exec(ctx, `
INSERT INTO admin_audit_events (
    audit_event_id,action_id,actor_principal,verified_spiffe_identity,roles_snapshot,permissions_snapshot,
    action,permission,target_type,target_id,reason,idempotency_key_sha256,request_id,correlation_id,outcome,occurred_at
) VALUES
('73000000-0000-4000-8000-000000000011','73000000-0000-4000-8000-000000000001','integration-ops',$1,
 ARRAY['operations'],ARRAY['notification.retry'],'notification.retry','notification.retry','notification',
 '73000000-0000-4000-8000-000000000002','retention integration',$2,'retention-request',
 '73000000-0000-4000-8000-000000000003','accepted',$3),
('73000000-0000-4000-8000-000000000012','73000000-0000-4000-8000-000000000001','integration-ops',$1,
 ARRAY['operations'],ARRAY['notification.retry'],'notification.retry','notification.retry','notification',
 '73000000-0000-4000-8000-000000000002','retention integration',$2,'retention-request',
 '73000000-0000-4000-8000-000000000003','accepted',$4)`,
		principal, strings.Repeat("a", 64), now.Add(-400*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDataset := runtimeStore.RetentionDatasets(365 * 24 * time.Hour)[0]
	if _, err := runtimeDataset.DeleteBatch(ctx, now.Add(-365*24*time.Hour), 10); err == nil {
		t.Fatal("admin runtime role deleted append-only audit data")
	}
	migratorDataset := migratorStore.RetentionDatasets(365 * 24 * time.Hour)[0]
	if deleted, err := migratorDataset.DeleteBatch(ctx, now.Add(-365*24*time.Hour), 10); err != nil || deleted != 1 {
		t.Fatalf("migrator retention delete = %d, %v", deleted, err)
	}
	var remaining int
	if err := migratorStore.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining admin audit events = %d, want 1", remaining)
	}
}
