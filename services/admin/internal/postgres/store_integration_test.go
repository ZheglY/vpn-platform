package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/admin/internal/application"
	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
	"github.com/ZheglY/vpn-platform/services/admin/internal/owner"
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
	action, claimed, err := runtimeStore.StartActionAttempt(ctx, input.ActionID, input.RequestID, time.Minute)
	if err != nil || !claimed || action.Status != "processing" || action.ClaimID == "" {
		t.Fatalf("start action attempt=%+v claimed=%v err=%v", action, claimed, err)
	}
	result := json.RawMessage(`{"notification_id":"20000000-0000-4000-8000-000000000001","status":"pending"}`)
	action, err = runtimeStore.CompleteAction(ctx, input.ActionID, action.ClaimID, input.RequestID, result, "")
	if err != nil || action.Status != "succeeded" {
		t.Fatalf("complete action=%+v err=%v", action, err)
	}
	events, err := runtimeStore.ListAudit(ctx, 10)
	if err != nil || len(events) != 3 || events[0].Outcome != "succeeded" || events[1].Outcome != "attempted" || events[2].Outcome != "accepted" {
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

func TestIntegrationDisabledAdminPrincipalLosesAccessImmediately(t *testing.T) {
	runtimeStore, cleanup := adminIntegrationStores(t)
	defer cleanup()
	ctx := context.Background()
	const spiffeID = "spiffe://vpn-service/ns/local/admin/integration-ops"

	if _, err := runtimeStore.GetPrincipal(ctx, spiffeID, "integration-ops"); err != nil {
		t.Fatalf("enabled principal: %v", err)
	}
	migratorStore, err := Open(ctx, os.Getenv("ADMIN_MIGRATOR_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer migratorStore.Close()
	if _, err := migratorStore.pool.Exec(ctx,
		`UPDATE admin_principals SET enabled=false WHERE spiffe_id=$1`, spiffeID,
	); err != nil {
		t.Fatalf("disable principal: %v", err)
	}
	if _, err := runtimeStore.GetPrincipal(ctx, spiffeID, "integration-ops"); err == nil {
		t.Fatal("disabled principal retained administrator access")
	}
}

func TestIntegrationAdminRetriesUnknownOwnerOutcomeWithStableIdentity(t *testing.T) {
	runtimeStore, cleanup := adminIntegrationStores(t)
	defer cleanup()
	const (
		subscriptionID = "20000000-0000-4000-8000-000000000041"
		reasonCode     = "admin_block"
	)
	var (
		mu               sync.Mutex
		mutationCount    int
		ownerKey         string
		ownerActionID    string
		ownerCorrelation string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
		if err != nil {
			t.Errorf("read owner request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var request struct {
			ActionID      string `json:"action_id"`
			CorrelationID string `json:"correlation_id"`
			ReasonCode    string `json:"reason_code"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode owner request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		mu.Lock()
		replay := ownerKey != ""
		if !replay {
			mutationCount++
			ownerKey, ownerActionID, ownerCorrelation = key, request.ActionID, request.CorrelationID
		} else if key != ownerKey || request.ActionID != ownerActionID || request.CorrelationID != ownerCorrelation {
			t.Errorf("owner retry identity changed: key=%q action=%q correlation=%q", key, request.ActionID, request.CorrelationID)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subscription_id": subscriptionID,
			"status":          "revoked",
			"reason_code":     reasonCode,
			"replay":          replay,
		})
	}))
	defer server.Close()
	clientHTTP := server.Client()
	clientHTTP.Transport = &dropFirstResponseTransport{base: clientHTTP.Transport}
	owners, err := owner.New(owner.Config{
		IdentityBaseURL: server.URL, BillingBaseURL: server.URL, SubscriptionBaseURL: server.URL,
		AccessBaseURL: server.URL, ProvisioningBaseURL: server.URL, NotificationBaseURL: server.URL,
	}, clientHTTP)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := runtimeStore.GetPrincipal(context.Background(), "spiffe://vpn-service/ns/local/admin/integration-ops", "integration-ops")
	if err != nil {
		t.Fatal(err)
	}
	service := application.New(runtimeStore)
	input := application.ExecuteInput{
		Principal: principal, Action: domain.ActionSubscriptionRevoke, Permission: "subscription.revoke",
		TargetType: "subscription", TargetID: subscriptionID, Reason: "revoke after verified abuse",
		ReasonCode: reasonCode, IdempotencyKey: "admin-unknown-outcome-0001",
		RequestSHA256: strings.Repeat("a", 64), RequestID: "request-unknown-1",
	}
	call := func(ctx context.Context, actionID, correlationID string) (any, error) {
		return owners.RevokeSubscription(ctx, subscriptionID, actionID, correlationID, reasonCode)
	}
	first, err := service.Execute(context.Background(), input, call)
	var firstOwnerErr *domain.OwnerError
	if !errors.As(err, &firstOwnerErr) || firstOwnerErr.Definitive || first.Status != "outcome_unknown" {
		t.Fatalf("first action=%+v err=%T %v", first, err, err)
	}
	input.RequestID = "request-unknown-2"
	second, err := service.Execute(context.Background(), input, call)
	if err != nil || second.Status != "succeeded" || second.ActionID != first.ActionID || second.CorrelationID != first.CorrelationID {
		t.Fatalf("recovered action=%+v err=%v", second, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if mutationCount != 1 || ownerKey != "admin-"+first.ActionID {
		t.Fatalf("mutations=%d owner_key=%q action_id=%q", mutationCount, ownerKey, first.ActionID)
	}
	events, err := runtimeStore.ListAudit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		got = append(got, events[i].Outcome)
	}
	want := []string{"accepted", "attempted", "outcome_unknown", "retrying", "succeeded"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit outcomes=%v want=%v", got, want)
	}
}

type dropFirstResponseTransport struct {
	base http.RoundTripper
	once sync.Once
}

func (t *dropFirstResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	drop := false
	t.once.Do(func() { drop = true })
	if !drop {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, io.ErrUnexpectedEOF
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
