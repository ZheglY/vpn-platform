package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
)

func TestIntegrationAdminActionIdempotencyAuditAndAppendOnlyRole(t *testing.T) {
	runtimeStore, cleanup := adminIntegrationStores(t)
	defer cleanup()
	ctx := context.Background()
	principal, err := runtimeStore.GetPrincipal(ctx, "spiffe://vpn-service/ns/local/admin/integration-ops", "integration-ops")
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ActionInput{
		ActionID: "10000000-0000-4000-8000-000000000001", Actor: principal,
		Action: domain.ActionNotificationRetry, Permission: "notification.retry", TargetType: "notification",
		TargetID: "20000000-0000-4000-8000-000000000001", Reason: "retry after outage",
		IdempotencyKeySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RequestSHA256:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequestID:            "request-integration-1", CorrelationID: "30000000-0000-4000-8000-000000000001",
	}
	action, replay, err := runtimeStore.BeginAction(ctx, input)
	if err != nil || replay || action.Status != "pending" {
		t.Fatalf("begin action=%+v replay=%v err=%v", action, replay, err)
	}
	action, replay, err = runtimeStore.BeginAction(ctx, input)
	if err != nil || !replay || action.Status != "pending" {
		t.Fatalf("pending crash replay=%+v replay=%v err=%v", action, replay, err)
	}
	collision := input
	collision.RequestSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, _, err := runtimeStore.BeginAction(ctx, collision); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("idempotency collision = %v", err)
	}
	result := json.RawMessage(`{"notification_id":"20000000-0000-4000-8000-000000000001","status":"pending"}`)
	action, err = runtimeStore.CompleteAction(ctx, input.ActionID, result, "")
	if err != nil || action.Status != "succeeded" {
		t.Fatalf("complete action=%+v err=%v", action, err)
	}
	events, err := runtimeStore.ListAudit(ctx, 10)
	if err != nil || len(events) != 2 || events[0].Outcome != "succeeded" || events[1].Outcome != "accepted" {
		t.Fatalf("audit=%+v err=%v", events, err)
	}
	if _, err := runtimeStore.pool.Exec(ctx, `UPDATE admin_audit_events SET outcome='failed' WHERE audit_event_id=$1`, events[0].AuditEventID); err == nil {
		t.Fatal("runtime role updated append-only audit")
	}
	if _, err := runtimeStore.pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE audit_event_id=$1`, events[0].AuditEventID); err == nil {
		t.Fatal("runtime role deleted append-only audit")
	}
	if action.CreatedAt.Before(time.Now().Add(-time.Minute)) || action.CompletedAt == nil {
		t.Fatalf("database timestamps are invalid: %+v", action)
	}
}

func adminIntegrationStores(t *testing.T) (*Store, func()) {
	t.Helper()
	runtimeDSN := os.Getenv("ADMIN_TEST_DATABASE_URL")
	migratorDSN := os.Getenv("ADMIN_MIGRATOR_TEST_DATABASE_URL")
	if runtimeDSN == "" || migratorDSN == "" {
		t.Skip("ADMIN_TEST_DATABASE_URL and ADMIN_MIGRATOR_TEST_DATABASE_URL are not set")
	}
	ownerStore, err := Open(context.Background(), migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerStore.pool.Exec(context.Background(), `TRUNCATE admin_audit_events, admin_action_requests, admin_role_grants, admin_principals CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := ownerStore.Bootstrap(context.Background(), []PrincipalSeed{{
		SPIFFEID: "spiffe://vpn-service/ns/local/admin/integration-ops", DisplayName: "Integration operations",
		Enabled: true, Roles: []string{"operations"},
	}}); err != nil {
		t.Fatal(err)
	}
	runtimeStore, err := Open(context.Background(), runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		runtimeStore.Close()
		_, _ = ownerStore.pool.Exec(context.Background(), `TRUNCATE admin_audit_events, admin_action_requests, admin_role_grants, admin_principals CASCADE`)
		ownerStore.Close()
	}
	return runtimeStore, cleanup
}
