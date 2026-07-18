package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestIntegrationAccessLifecycleAndTokenRotation(t *testing.T) {
	dsn := os.Getenv("ACCESS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACCESS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.pool.Exec(ctx, `TRUNCATE outbox, consumer_dead_letters, inbox, url_idempotency, subscription_tokens, access_operations, access_endpoint_snapshots, access_credentials CASCADE`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := store.pool.Exec(context.Background(), `TRUNCATE outbox, consumer_dead_letters, inbox, url_idempotency, subscription_tokens, access_operations, access_endpoint_snapshots, access_credentials CASCADE`); err != nil {
			t.Errorf("clean access integration state: %v", err)
		}
	}()

	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "10000000-0000-4000-8000-000000000001"
	userID := "10000000-0000-4000-8000-000000000002"
	credentialID := "10000000-0000-4000-8000-000000000003"
	operationID := "10000000-0000-4000-8000-000000000004"
	periodMeta := integrationMeta("10000000-0000-4000-8000-000000000005", "subscription.activated.v1", subscriptionID, 1)
	period := domain.PeriodEvent{SubscriptionID: subscriptionID, UserID: userID, PeriodID: "10000000-0000-4000-8000-000000000006", SourceOrderID: "10000000-0000-4000-8000-000000000007", SourcePaymentID: "10000000-0000-4000-8000-000000000008", PeriodStart: now, PeriodEnd: now.Add(30 * 24 * time.Hour), GraceEndsAt: now.Add(31 * 24 * time.Hour)}
	seed := domain.CredentialSeed{CredentialID: credentialID, OperationID: operationID, Ciphertext: bytes.Repeat([]byte{0x42}, 64), KeyVersion: 1}
	if err := store.ApplyPeriod(ctx, periodMeta, period, seed); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPeriod(ctx, periodMeta, period, seed); err != nil {
		t.Fatalf("duplicate period: %v", err)
	}
	assertCount(t, store, "access_credentials", 1)
	assertCount(t, store, "inbox", 1)
	assertCount(t, store, "outbox", 1)

	provisionMeta := integrationMeta("20000000-0000-4000-8000-000000000001", "access.provision.succeeded.v1", credentialID, 2)
	result := domain.ProvisionSucceeded{
		OperationID: operationID, CredentialID: credentialID, AppliedRevision: 1, Status: domain.StatusActive, AppliedAt: now.Add(time.Second),
		Endpoints: []domain.EndpointSnapshot{
			{NodeID: "20000000-0000-4000-8000-000000000002", Role: "primary", Address: "vpn.example.invalid", Port: 443, ServerName: "cdn.example.invalid", RealityPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0011", Label: "Primary"},
			{NodeID: "20000000-0000-4000-8000-000000000003", Role: "failover", Address: "backup.example.invalid", Port: 443, ServerName: "www.example.invalid", RealityPublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", ShortID: "2233", Label: "Failover"},
		},
	}
	if err := store.ApplyProvisionSucceeded(ctx, provisionMeta, result); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyProvisionSucceeded(ctx, provisionMeta, result); err != nil {
		t.Fatalf("duplicate provision result: %v", err)
	}
	delayedFailureMeta := integrationMeta("20000000-0000-4000-8000-000000000004", "access.provision.failed.v1", credentialID, 20)
	delayedFailure := domain.OperationFailed{OperationID: operationID, CredentialID: credentialID, FailedRevision: 1, FailureScope: "all", Terminal: true, ReasonCode: "delayed_failure", FailedAt: now.Add(2 * time.Second)}
	if err := store.ApplyOperationFailed(ctx, delayedFailureMeta, delayedFailure, "provision"); err != nil {
		t.Fatalf("apply delayed failure: %v", err)
	}
	var operationStatus, credentialStatus string
	if err := store.pool.QueryRow(ctx, `SELECT o.status, c.status FROM access_operations o JOIN access_credentials c ON c.id=o.credential_id WHERE o.id=$1`, operationID).Scan(&operationStatus, &credentialStatus); err != nil {
		t.Fatal(err)
	}
	if operationStatus != "succeeded" || credentialStatus != domain.StatusActive {
		t.Fatalf("delayed failure downgraded state: operation=%s credential=%s", operationStatus, credentialStatus)
	}
	status, err := store.GetAccessStatus(ctx, subscriptionID)
	if err != nil || status.AccessStatus != "ready" || status.ProvisioningStatus != "active" || status.TokenStatus != "not_issued" {
		t.Fatalf("unexpected ready status: %#v, %v", status, err)
	}

	firstLookup := bytes.Repeat([]byte{1}, 32)
	firstSeed := domain.TokenSeed{TokenID: "30000000-0000-4000-8000-000000000001", LookupHMAC: firstLookup, IdempotencyKey: "issue-request-0001", RequestSHA256: strings.Repeat("a", 64)}
	if err := store.IssueToken(ctx, subscriptionID, "issue", firstSeed); err != nil {
		t.Fatal(err)
	}
	if err := store.IssueToken(ctx, subscriptionID, "issue", firstSeed); !errors.Is(err, domain.ErrIdempotencyReplay) {
		t.Fatalf("issue replay = %v", err)
	}
	secondLookup := bytes.Repeat([]byte{2}, 32)
	secondSeed := domain.TokenSeed{TokenID: "30000000-0000-4000-8000-000000000002", LookupHMAC: secondLookup, IdempotencyKey: "rotate-request-001", RequestSHA256: strings.Repeat("b", 64)}
	if err := store.IssueToken(ctx, subscriptionID, "rotate", secondSeed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProfileByTokenHMAC(ctx, firstLookup); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rotated token lookup = %v", err)
	}
	profile, err := store.GetProfileByTokenHMAC(ctx, secondLookup)
	if err != nil || len(profile.Endpoints) != 2 || profile.CredentialID != credentialID {
		t.Fatalf("active profile = %#v, %v", profile, err)
	}

	terminalMeta := integrationMeta("40000000-0000-4000-8000-000000000001", "subscription.expired.v1", subscriptionID, 3)
	terminal := domain.TerminalEvent{SubscriptionID: subscriptionID, UserID: userID, Reason: "expired", EffectiveAt: period.GraceEndsAt}
	if err := store.ApplyTerminal(ctx, terminalMeta, terminal, "40000000-0000-4000-8000-000000000002"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProfileByTokenHMAC(ctx, secondLookup); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked token lookup = %v", err)
	}
	status, err = store.GetAccessStatus(ctx, subscriptionID)
	if err != nil || status.AccessStatus != "revoking" || status.TokenStatus != "revoked" {
		t.Fatalf("unexpected revoking status: %#v, %v", status, err)
	}

	reactivationMeta := integrationMeta("50000000-0000-4000-8000-000000000001", "subscription.activated.v1", subscriptionID, 4)
	reactivationPeriod := period
	reactivationPeriod.PeriodID = "50000000-0000-4000-8000-000000000002"
	reactivationPeriod.PeriodEnd = period.PeriodEnd.Add(30 * 24 * time.Hour)
	reactivationPeriod.GraceEndsAt = period.GraceEndsAt.Add(30 * 24 * time.Hour)
	replacementSeed := domain.CredentialSeed{CredentialID: "50000000-0000-4000-8000-000000000003", OperationID: "50000000-0000-4000-8000-000000000004", Ciphertext: bytes.Repeat([]byte{0x24}, 64), KeyVersion: 2}
	if err := store.ApplyPeriod(ctx, reactivationMeta, reactivationPeriod, replacementSeed); err != nil {
		t.Fatalf("reactivate access: %v", err)
	}
	var currentCredentialID string
	var currentCiphertext []byte
	if err := store.pool.QueryRow(ctx, `SELECT id, vless_uuid_ciphertext FROM access_credentials WHERE subscription_id=$1 AND status <> 'revoked'`, subscriptionID).Scan(&currentCredentialID, &currentCiphertext); err != nil {
		t.Fatal(err)
	}
	if currentCredentialID != credentialID || !bytes.Equal(currentCiphertext, seed.Ciphertext) {
		t.Fatal("reactivation replaced credential material bound to the existing credential ID")
	}

	message, ok, err := store.ClaimOutbox(ctx, 10*time.Second)
	if err != nil || !ok || message.Topic != "access.provision.request.v1" {
		t.Fatalf("first outbox claim: %#v, %t, %v", message, ok, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	encoded := string(message.Payload)
	if strings.Contains(encoded, "vless_client_uuid") || strings.Contains(encoded, "subscription_url") || strings.Contains(encoded, "token") {
		t.Fatalf("secret field leaked into outbox: %s", encoded)
	}
}

func integrationMeta(eventID, eventType, aggregateID string, offset int64) domain.EventMeta {
	payloadHash := sha256.Sum256([]byte(eventID + "\n" + eventType))
	return domain.EventMeta{EventID: eventID, EventType: eventType, AggregateID: aggregateID, CorrelationID: "90000000-0000-4000-8000-000000000001", SourceTopic: eventType, SourcePartition: 0, SourceOffset: offset, PayloadSHA256: hex.EncodeToString(payloadHash[:])}
}

func assertCount(t *testing.T, store *Store, table string, want int) {
	t.Helper()
	var got int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
