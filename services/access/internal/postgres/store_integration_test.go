package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestIntegrationAccessLifecycleAndTokenRotation(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)

	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "10000000-0000-4000-8000-000000000001"
	userID := "10000000-0000-4000-8000-000000000002"
	credentialID := "10000000-0000-4000-8000-000000000003"
	operationID := "10000000-0000-4000-8000-000000000004"
	periodMeta := integrationMeta("10000000-0000-4000-8000-000000000005", "subscription.activated.v1", subscriptionID, 1, 1)
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

	provisionMeta := integrationMeta("20000000-0000-4000-8000-000000000001", "access.provision.succeeded.v1", credentialID, 2, 1)
	result := domain.ProvisionSucceeded{
		OperationID: operationID, CredentialID: credentialID, AppliedRevision: 1, Status: domain.StatusActive, AppliedAt: now.Add(time.Second),
		Endpoints: []domain.EndpointSnapshot{
			{NodeID: "20000000-0000-4000-8000-000000000002", Role: "primary", Address: "vpn.example.invalid", Port: 443, ServerName: "cdn.example.invalid", RealityPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0011", Label: "Primary"},
			{NodeID: "20000000-0000-4000-8000-000000000003", Role: "failover", Address: "backup.example.invalid", Port: 443, ServerName: "www.example.invalid", RealityPublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", ShortID: "2233", Label: "Failover"},
		},
	}
	result.AssignedNodeIDs = []string{result.Endpoints[0].NodeID, result.Endpoints[1].NodeID}
	if err := store.ApplyProvisionSucceeded(ctx, provisionMeta, result); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyProvisionSucceeded(ctx, provisionMeta, result); err != nil {
		t.Fatalf("duplicate provision result: %v", err)
	}
	delayedFailureMeta := integrationMeta("20000000-0000-4000-8000-000000000004", "access.provision.failed.v1", credentialID, 20, 2)
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
	firstSeed := domain.TokenSeed{TokenID: "30000000-0000-4000-8000-000000000001", LookupHMAC: firstLookup, LookupHMACVersion: 1, IdempotencyKey: "issue-request-0001", RequestSHA256: strings.Repeat("a", 64)}
	if err := store.IssueToken(ctx, subscriptionID, "issue", firstSeed); err != nil {
		t.Fatal(err)
	}
	if err := store.IssueToken(ctx, subscriptionID, "issue", firstSeed); !errors.Is(err, domain.ErrIdempotencyReplay) {
		t.Fatalf("issue replay = %v", err)
	}
	secondLookup := bytes.Repeat([]byte{2}, 32)
	secondSeed := domain.TokenSeed{TokenID: "30000000-0000-4000-8000-000000000002", LookupHMAC: secondLookup, LookupHMACVersion: 1, IdempotencyKey: "rotate-request-001", RequestSHA256: strings.Repeat("b", 64)}
	if err := store.IssueToken(ctx, subscriptionID, "rotate", secondSeed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProfileByTokenHMACs(ctx, [][]byte{firstLookup}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rotated token lookup = %v", err)
	}
	profile, err := store.GetProfileByTokenHMACs(ctx, [][]byte{secondLookup})
	if err != nil || len(profile.Endpoints) != 2 || profile.CredentialID != credentialID {
		t.Fatalf("active profile = %#v, %v", profile, err)
	}

	terminalMeta := integrationMeta("40000000-0000-4000-8000-000000000001", "subscription.expired.v1", subscriptionID, 3, 2)
	terminal := domain.TerminalEvent{SubscriptionID: subscriptionID, UserID: userID, Reason: "expired", EffectiveAt: period.GraceEndsAt}
	if err := store.ApplyTerminal(ctx, terminalMeta, terminal, "40000000-0000-4000-8000-000000000002"); err != nil {
		t.Fatal(err)
	}
	var revokeCommandPayload []byte
	if err := store.pool.QueryRow(ctx, `SELECT payload FROM outbox WHERE topic='access.revoke.request.v1'`).Scan(&revokeCommandPayload); err != nil {
		t.Fatal(err)
	}
	var revokeCommandEnvelope struct {
		AggregateSequence int64 `json:"aggregate_sequence"`
	}
	if err := json.Unmarshal(revokeCommandPayload, &revokeCommandEnvelope); err != nil || revokeCommandEnvelope.AggregateSequence != 2 {
		t.Fatalf("revoke command sequence = %d, %v", revokeCommandEnvelope.AggregateSequence, err)
	}
	if _, err := store.GetProfileByTokenHMACs(ctx, [][]byte{secondLookup}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked token lookup = %v", err)
	}
	status, err = store.GetAccessStatus(ctx, subscriptionID)
	if err != nil || status.AccessStatus != "revoking" || status.TokenStatus != "revoked" {
		t.Fatalf("unexpected revoking status: %#v, %v", status, err)
	}

	reactivationMeta := integrationMeta("50000000-0000-4000-8000-000000000001", "subscription.activated.v1", subscriptionID, 4, 3)
	reactivationPeriod := period
	reactivationPeriod.PeriodID = "50000000-0000-4000-8000-000000000002"
	reactivationPeriod.SourceOrderID = "50000000-0000-4000-8000-000000000005"
	reactivationPeriod.SourcePaymentID = "50000000-0000-4000-8000-000000000006"
	reactivationPeriod.PeriodEnd = period.PeriodEnd.Add(30 * 24 * time.Hour)
	reactivationPeriod.GraceEndsAt = period.GraceEndsAt.Add(30 * 24 * time.Hour)
	replacementSeed := domain.CredentialSeed{CredentialID: "50000000-0000-4000-8000-000000000003", OperationID: "50000000-0000-4000-8000-000000000004", Ciphertext: bytes.Repeat([]byte{0x24}, 64), KeyVersion: 2}
	if err := store.ApplyPeriod(ctx, reactivationMeta, reactivationPeriod, replacementSeed); err != nil {
		t.Fatalf("reactivate access: %v", err)
	}
	assertCount(t, store, "payment_access_sli", 2)
	var currentCredentialID string
	var currentCiphertext []byte
	if err := store.pool.QueryRow(ctx, `SELECT id, vless_uuid_ciphertext FROM access_credentials WHERE subscription_id=$1 AND status <> 'revoked'`, subscriptionID).Scan(&currentCredentialID, &currentCiphertext); err != nil {
		t.Fatal(err)
	}
	if currentCredentialID != credentialID || !bytes.Equal(currentCiphertext, seed.Ciphertext) {
		t.Fatal("reactivation replaced credential material bound to the existing credential ID")
	}

	if _, err := store.pool.Exec(ctx, `UPDATE outbox SET created_at = clock_timestamp() - aggregate_sequence * interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	message, ok, err := store.ClaimOutbox(ctx, 10*time.Second)
	if err != nil || !ok || message.Topic != "access.provision.request.v1" {
		t.Fatalf("first outbox claim: %#v, %t, %v", message, ok, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["aggregate_sequence"] != float64(1) {
		t.Fatalf("first outbox aggregate sequence = %v", payload["aggregate_sequence"])
	}
	encoded := string(message.Payload)
	if strings.Contains(encoded, "vless_client_uuid") || strings.Contains(encoded, "subscription_url") || strings.Contains(encoded, "token") {
		t.Fatalf("secret field leaked into outbox: %s", encoded)
	}
}

func TestIntegrationLifecycleSequenceGapAndStaleEvent(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "61000000-0000-4000-8000-000000000001"
	userID := "61000000-0000-4000-8000-000000000002"
	period := domain.PeriodEvent{
		SubscriptionID: subscriptionID, UserID: userID,
		PeriodID: "61000000-0000-4000-8000-000000000003", SourceOrderID: "61000000-0000-4000-8000-000000000004", SourcePaymentID: "61000000-0000-4000-8000-000000000005",
		PeriodStart: now, PeriodEnd: now.Add(24 * time.Hour), GraceEndsAt: now.Add(25 * time.Hour),
	}
	seed := domain.CredentialSeed{CredentialID: "61000000-0000-4000-8000-000000000006", OperationID: "61000000-0000-4000-8000-000000000007", Ciphertext: bytes.Repeat([]byte{0x61}, 64), KeyVersion: 1}
	terminalMeta := integrationMeta("61000000-0000-4000-8000-000000000008", "subscription.expired.v1", subscriptionID, 2, 2)
	terminal := domain.TerminalEvent{SubscriptionID: subscriptionID, UserID: userID, Reason: "expired", EffectiveAt: period.GraceEndsAt}
	if err := store.ApplyTerminal(ctx, terminalMeta, terminal, "61000000-0000-4000-8000-000000000009"); !errors.Is(err, domain.ErrLifecycleSequenceGap) {
		t.Fatalf("out-of-order terminal = %v", err)
	}
	assertCount(t, store, "inbox", 0)
	assertCount(t, store, "subscription_lifecycle_state", 0)

	activationMeta := integrationMeta("61000000-0000-4000-8000-000000000010", "subscription.activated.v1", subscriptionID, 1, 1)
	if err := store.ApplyPeriod(ctx, activationMeta, period, seed); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyTerminal(ctx, terminalMeta, terminal, "61000000-0000-4000-8000-000000000009"); err != nil {
		t.Fatal(err)
	}
	var lastApplied int64
	if err := store.pool.QueryRow(ctx, `SELECT last_applied_sequence FROM subscription_lifecycle_state WHERE subscription_id=$1`, subscriptionID).Scan(&lastApplied); err != nil {
		t.Fatal(err)
	}
	if lastApplied != 2 {
		t.Fatalf("last applied sequence = %d", lastApplied)
	}
	staleMeta := integrationMeta("61000000-0000-4000-8000-000000000011", "subscription.activated.v1", subscriptionID, 3, 1)
	if err := store.ApplyPeriod(ctx, staleMeta, period, seed); !errors.Is(err, domain.ErrDurableStateConflict) {
		t.Fatalf("stale lifecycle event = %v", err)
	}
	endpoints := []domain.EndpointSnapshot{
		{NodeID: "61000000-0000-4000-8000-000000000012", Role: "primary", Address: "primary.example.invalid", Port: 443, ServerName: "cdn.example.invalid", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "Primary"},
		{NodeID: "61000000-0000-4000-8000-000000000013", Role: "failover", Address: "failover.example.invalid", Port: 443, ServerName: "www.example.invalid", RealityPublicKey: strings.Repeat("B", 43), ShortID: "2233", Label: "Failover"},
	}
	lateSuccess := domain.ProvisionSucceeded{OperationID: seed.OperationID, CredentialID: seed.CredentialID, AppliedRevision: 1, Status: domain.StatusActive, AssignedNodeIDs: []string{endpoints[0].NodeID, endpoints[1].NodeID}, Endpoints: endpoints, AppliedAt: now}
	if err := store.ApplyProvisionSucceeded(ctx, integrationMeta("61000000-0000-4000-8000-000000000014", "access.provision.succeeded.v1", seed.CredentialID, 4, 1), lateSuccess); err != nil {
		t.Fatalf("late provision while revoking: %v", err)
	}
	var credentialStatus string
	var credentialAllocation, operationAllocation int
	if err := store.pool.QueryRow(ctx, `
SELECT c.status, c.allocation_revision, o.allocation_revision
FROM access_credentials c JOIN access_operations o ON o.credential_id=c.id AND o.kind='revoke'
WHERE c.id=$1`, seed.CredentialID).Scan(&credentialStatus, &credentialAllocation, &operationAllocation); err != nil {
		t.Fatal(err)
	}
	if credentialStatus != domain.StatusRevoking || credentialAllocation != 1 || operationAllocation != 1 {
		t.Fatalf("late allocation state = %s credential=%d operation=%d", credentialStatus, credentialAllocation, operationAllocation)
	}
	var readyCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic='access.ready.v1'`).Scan(&readyCount); err != nil || readyCount != 0 {
		t.Fatalf("late provision emitted readiness: count=%d err=%v", readyCount, err)
	}
	revokeResult := domain.RevokeSucceeded{
		OperationID: "61000000-0000-4000-8000-000000000009", CredentialID: seed.CredentialID,
		DesiredRevision: 2, AllocationRevision: 1, AllAssignedNodesRemoved: true,
		NodeIDs: []string{endpoints[0].NodeID, endpoints[1].NodeID}, RevokedAt: now,
	}
	if err := store.ApplyRevokeSucceeded(ctx, integrationMeta("61000000-0000-4000-8000-000000000015", "access.revoke.succeeded.v1", seed.CredentialID, 5, 2), revokeResult); err != nil {
		t.Fatalf("revoke late allocation: %v", err)
	}
}

func TestIntegrationReorderedProvisionAndRevokeOutcomesConvergeBySequence(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	unknown := domain.OperationFailed{
		OperationID: "61500000-0000-4000-8000-000000000030", CredentialID: "61500000-0000-4000-8000-000000000031",
		FailedRevision: 1, FailureScope: "primary", Terminal: true, ReasonCode: "unknown_credential", FailedAt: now,
	}
	if err := store.ApplyOperationFailed(ctx, integrationMeta("61500000-0000-4000-8000-000000000032", "access.provision.failed.v1", unknown.CredentialID, 1, 1), unknown, "provision"); !errors.Is(err, domain.ErrDurableStateConflict) {
		t.Fatalf("unknown outcome = %v", err)
	}
	assertCount(t, store, "provisioning_outcome_state", 0)
	assertCount(t, store, "inbox", 0)
	subscriptionID := "61500000-0000-4000-8000-000000000001"
	userID := "61500000-0000-4000-8000-000000000002"
	credentialID := "61500000-0000-4000-8000-000000000003"
	provisionOperationID := "61500000-0000-4000-8000-000000000004"
	period := domain.PeriodEvent{
		SubscriptionID: subscriptionID, UserID: userID,
		PeriodID: "61500000-0000-4000-8000-000000000005", SourceOrderID: "61500000-0000-4000-8000-000000000006", SourcePaymentID: "61500000-0000-4000-8000-000000000007",
		PeriodStart: now, PeriodEnd: now.Add(time.Hour), GraceEndsAt: now.Add(2 * time.Hour),
	}
	if err := store.ApplyPeriod(ctx, integrationMeta("61500000-0000-4000-8000-000000000008", "subscription.activated.v1", subscriptionID, 1, 1), period,
		domain.CredentialSeed{CredentialID: credentialID, OperationID: provisionOperationID, Ciphertext: bytes.Repeat([]byte{0x61}, 64), KeyVersion: 1}); err != nil {
		t.Fatal(err)
	}
	revokeOperationID := "61500000-0000-4000-8000-000000000009"
	terminal := domain.TerminalEvent{SubscriptionID: subscriptionID, UserID: userID, Reason: "refund", EffectiveAt: now}
	if err := store.ApplyTerminal(ctx, integrationMeta("61500000-0000-4000-8000-000000000010", "subscription.revoked.v1", subscriptionID, 2, 2), terminal, revokeOperationID); err != nil {
		t.Fatal(err)
	}
	nodeIDs := []string{"61500000-0000-4000-8000-000000000011", "61500000-0000-4000-8000-000000000012"}
	revoke := domain.RevokeSucceeded{OperationID: revokeOperationID, CredentialID: credentialID, DesiredRevision: 2, AllocationRevision: 1, AllAssignedNodesRemoved: true, NodeIDs: nodeIDs, RevokedAt: now}
	revokeMeta := integrationMeta("61500000-0000-4000-8000-000000000013", "access.revoke.succeeded.v1", credentialID, 20, 2)
	if err := store.ApplyRevokeSucceeded(ctx, revokeMeta, revoke); !errors.Is(err, domain.ErrOutcomeSequenceGap) {
		t.Fatalf("reordered revoke outcome = %v", err)
	}

	degraded := domain.ProvisionSucceeded{
		OperationID: provisionOperationID, CredentialID: credentialID, AppliedRevision: 1, Status: domain.StatusDegraded,
		AssignedNodeIDs: nodeIDs,
		Endpoints:       []domain.EndpointSnapshot{{NodeID: nodeIDs[0], Role: "primary", Address: "primary.example.invalid", Port: 443, ServerName: "cdn.example.invalid", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "Primary"}},
		AppliedAt:       now,
	}
	if err := store.ApplyProvisionSucceeded(ctx, integrationMeta("61500000-0000-4000-8000-000000000014", "access.provision.succeeded.v1", credentialID, 10, 1), degraded); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRevokeSucceeded(ctx, revokeMeta, revoke); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRevokeSucceeded(ctx, revokeMeta, revoke); err != nil {
		t.Fatalf("exact outcome replay: %v", err)
	}
	collision := integrationMeta("61500000-0000-4000-8000-000000000015", "access.revoke.failed.v1", credentialID, 21, 2)
	failure := domain.OperationFailed{OperationID: revokeOperationID, CredentialID: credentialID, FailedRevision: 2, FailureScope: "all", Terminal: true, ReasonCode: "collision", FailedAt: now}
	if err := store.ApplyOperationFailed(ctx, collision, failure, "revoke"); !errors.Is(err, domain.ErrDurableStateConflict) {
		t.Fatalf("outcome sequence collision = %v", err)
	}
	var status string
	var outcomeSequence int64
	if err := store.pool.QueryRow(ctx, `SELECT status FROM access_credentials WHERE id=$1`, credentialID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT last_applied_sequence FROM provisioning_outcome_state WHERE credential_id=$1`, credentialID).Scan(&outcomeSequence); err != nil {
		t.Fatal(err)
	}
	if status != domain.StatusRevoked || outcomeSequence != 2 {
		t.Fatalf("reordered outcome state = %s sequence=%d", status, outcomeSequence)
	}
}

func TestIntegrationDelayedProvisioningAndCompleteRevokeProof(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "62000000-0000-4000-8000-000000000001"
	userID := "62000000-0000-4000-8000-000000000002"
	credentialID := "62000000-0000-4000-8000-000000000003"
	provisionOperationID := "62000000-0000-4000-8000-000000000004"
	period := domain.PeriodEvent{
		SubscriptionID: subscriptionID, UserID: userID,
		PeriodID: "62000000-0000-4000-8000-000000000005", SourceOrderID: "62000000-0000-4000-8000-000000000006", SourcePaymentID: "62000000-0000-4000-8000-000000000007",
		PeriodStart: now, PeriodEnd: now.Add(time.Hour), GraceEndsAt: now.Add(2 * time.Hour),
	}
	if err := store.ApplyPeriod(ctx, integrationMeta("62000000-0000-4000-8000-000000000008", "subscription.activated.v1", subscriptionID, 1, 1), period,
		domain.CredentialSeed{CredentialID: credentialID, OperationID: provisionOperationID, Ciphertext: bytes.Repeat([]byte{0x62}, 64), KeyVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE access_credentials SET entitlement_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, credentialID); err != nil {
		t.Fatal(err)
	}
	endpoints := []domain.EndpointSnapshot{
		{NodeID: "62000000-0000-4000-8000-000000000009", Role: "primary", Address: "primary.example.invalid", Port: 443, ServerName: "cdn.example.invalid", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "Primary"},
		{NodeID: "62000000-0000-4000-8000-000000000010", Role: "failover", Address: "failover.example.invalid", Port: 443, ServerName: "www.example.invalid", RealityPublicKey: strings.Repeat("B", 43), ShortID: "2233", Label: "Failover"},
	}
	result := domain.ProvisionSucceeded{OperationID: provisionOperationID, CredentialID: credentialID, AppliedRevision: 1, Status: domain.StatusActive, AssignedNodeIDs: []string{endpoints[0].NodeID, endpoints[1].NodeID}, Endpoints: endpoints, AppliedAt: now}
	if err := store.ApplyProvisionSucceeded(ctx, integrationMeta("62000000-0000-4000-8000-000000000011", "access.provision.succeeded.v1", credentialID, 2, 1), result); err != nil {
		t.Fatal(err)
	}
	var credentialStatus, revokeOperationID string
	var revision, allocationRevision int
	if err := store.pool.QueryRow(ctx, `
SELECT c.status, c.credential_version, c.allocation_revision, o.id
FROM access_credentials c JOIN access_operations o ON o.credential_id=c.id AND o.kind='revoke'
WHERE c.id=$1`, credentialID).Scan(&credentialStatus, &revision, &allocationRevision, &revokeOperationID); err != nil {
		t.Fatal(err)
	}
	if credentialStatus != domain.StatusRevoking || revision != 2 || allocationRevision != 1 {
		t.Fatalf("delayed success state = %s revision=%d allocation=%d", credentialStatus, revision, allocationRevision)
	}
	var readyCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic='access.ready.v1'`).Scan(&readyCount); err != nil || readyCount != 0 {
		t.Fatalf("ready outbox count = %d, %v", readyCount, err)
	}
	partial := domain.RevokeSucceeded{OperationID: revokeOperationID, CredentialID: credentialID, DesiredRevision: 2, AllocationRevision: 1, AllAssignedNodesRemoved: true, NodeIDs: []string{endpoints[0].NodeID}, RevokedAt: now}
	if err := store.ApplyRevokeSucceeded(ctx, integrationMeta("62000000-0000-4000-8000-000000000012", "access.revoke.succeeded.v1", credentialID, 3, 2), partial); !errors.Is(err, domain.ErrDurableStateConflict) {
		t.Fatalf("partial revoke proof = %v", err)
	}
	complete := partial
	complete.NodeIDs = []string{endpoints[1].NodeID, endpoints[0].NodeID}
	if err := store.ApplyRevokeSucceeded(ctx, integrationMeta("62000000-0000-4000-8000-000000000013", "access.revoke.succeeded.v1", credentialID, 4, 2), complete); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT status FROM access_credentials WHERE id=$1`, credentialID).Scan(&credentialStatus); err != nil || credentialStatus != domain.StatusRevoked {
		t.Fatalf("complete revoke state = %s, %v", credentialStatus, err)
	}
}

func TestIntegrationZeroAllocationRevokeAndSecurityAudit(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC()
	credentialID := "63000000-0000-4000-8000-000000000001"
	operationID := "63000000-0000-4000-8000-000000000002"
	if _, err := store.pool.Exec(ctx, `
INSERT INTO access_credentials (id,subscription_id,user_id,status,credential_version,vless_uuid_ciphertext,encryption_key_version,entitlement_expires_at,allocation_revision,revocation_reason)
VALUES ($1,$2,$3,'revoking',2,$4,1,$5,0,'expired')`, credentialID, "63000000-0000-4000-8000-000000000003", "63000000-0000-4000-8000-000000000004", bytes.Repeat([]byte{0x63}, 64), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
INSERT INTO access_operations (id,credential_id,kind,desired_revision,allocation_revision,status,causation_event_id)
VALUES ($1,$2,'revoke',2,0,'pending',$3)`, operationID, credentialID, "63000000-0000-4000-8000-000000000005"); err != nil {
		t.Fatal(err)
	}
	event := domain.RevokeSucceeded{OperationID: operationID, CredentialID: credentialID, DesiredRevision: 2, AllocationRevision: 0, AllAssignedNodesRemoved: true, NodeIDs: []string{}, RevokedAt: now}
	if err := store.ApplyRevokeSucceeded(ctx, integrationMeta("63000000-0000-4000-8000-000000000006", "access.revoke.succeeded.v1", credentialID, 1, 1), event); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCredentialMaterialAccess(ctx, credentialID, "provisioning-service"); err != nil {
		t.Fatal(err)
	}
	var actor, action string
	if err := store.pool.QueryRow(ctx, `SELECT actor_service, action FROM security_audit_events WHERE credential_id=$1`, credentialID).Scan(&actor, &action); err != nil {
		t.Fatal(err)
	}
	if actor != "provisioning-service" || action != "credential_material.read" {
		t.Fatalf("security audit = %s %s", actor, action)
	}
}

func TestIntegrationGlobalURLIdempotencyKeyIsSerialized(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC()
	credentials := []struct{ credentialID, subscriptionID, userID string }{
		{"64000000-0000-4000-8000-000000000001", "64000000-0000-4000-8000-000000000002", "64000000-0000-4000-8000-000000000003"},
		{"64000000-0000-4000-8000-000000000004", "64000000-0000-4000-8000-000000000005", "64000000-0000-4000-8000-000000000006"},
	}
	for _, item := range credentials {
		if _, err := store.pool.Exec(ctx, `
INSERT INTO access_credentials (id,subscription_id,user_id,status,credential_version,vless_uuid_ciphertext,encryption_key_version,entitlement_expires_at)
VALUES ($1,$2,$3,'active',1,$4,1,$5)`, item.credentialID, item.subscriptionID, item.userID, bytes.Repeat([]byte{0x64}, 64), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(credentials))
	var wg sync.WaitGroup
	for index, item := range credentials {
		wg.Add(1)
		go func(index int, subscriptionID string) {
			defer wg.Done()
			<-start
			results <- store.IssueToken(ctx, subscriptionID, "issue", domain.TokenSeed{
				TokenID: fmt.Sprintf("64000000-0000-4000-8000-%012d", 100+index), LookupHMAC: bytes.Repeat([]byte{byte(index + 1)}, 32), LookupHMACVersion: 1,
				IdempotencyKey: "global-request-key", RequestSHA256: strings.Repeat(string(rune('a'+index)), 64),
			})
		}(index, item.subscriptionID)
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrIdempotencyConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent idempotency result: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("idempotency outcomes: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestIntegrationAccessStatusDoesNotReportReadyAfterEntitlementExpiry(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	subscriptionID := "65000000-0000-4000-8000-000000000001"
	if _, err := store.pool.Exec(ctx, `
INSERT INTO access_credentials (id,subscription_id,user_id,status,credential_version,vless_uuid_ciphertext,encryption_key_version,entitlement_expires_at)
VALUES ($1,$2,$3,'active',1,$4,1,clock_timestamp()-interval '1 second')`,
		"65000000-0000-4000-8000-000000000002", subscriptionID, "65000000-0000-4000-8000-000000000003", bytes.Repeat([]byte{0x65}, 64)); err != nil {
		t.Fatal(err)
	}
	status, err := store.GetAccessStatus(ctx, subscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.AccessStatus != "expired" || status.AccessStatus == "ready" {
		t.Fatalf("expired entitlement access status = %#v", status)
	}
}

func TestIntegrationTerminalFailureEmitsOrderedFactAndAdminRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := integrationStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "66000000-0000-4000-8000-000000000001"
	userID := "66000000-0000-4000-8000-000000000002"
	credentialID := "66000000-0000-4000-8000-000000000003"
	operationID := "66000000-0000-4000-8000-000000000004"
	period := domain.PeriodEvent{
		SubscriptionID: subscriptionID, UserID: userID,
		PeriodID: "66000000-0000-4000-8000-000000000005", SourceOrderID: "66000000-0000-4000-8000-000000000006", SourcePaymentID: "66000000-0000-4000-8000-000000000007",
		PeriodStart: now, PeriodEnd: now.Add(time.Hour), GraceEndsAt: now.Add(2 * time.Hour),
	}
	seed := domain.CredentialSeed{CredentialID: credentialID, OperationID: operationID, Ciphertext: bytes.Repeat([]byte{0x66}, 64), KeyVersion: 1}
	if err := store.ApplyPeriod(ctx, integrationMeta("66000000-0000-4000-8000-000000000008", "subscription.activated.v1", subscriptionID, 1, 1), period, seed); err != nil {
		t.Fatal(err)
	}
	failure := domain.OperationFailed{
		OperationID: operationID, CredentialID: credentialID, FailedRevision: 1, FailureScope: "all", Terminal: true,
		ReasonCode: "node_capacity_exhausted", FailedAt: now.Add(time.Second),
	}
	if err := store.ApplyOperationFailed(ctx, integrationMeta("66000000-0000-4000-8000-000000000009", "access.provision.failed.v1", credentialID, 2, 1), failure, "provision"); err != nil {
		t.Fatal(err)
	}
	var factPayload []byte
	if err := store.pool.QueryRow(ctx, `SELECT payload FROM outbox WHERE topic='access.provisioning.failed.v1'`).Scan(&factPayload); err != nil {
		t.Fatal(err)
	}
	var fact struct {
		AggregateSequence int64 `json:"aggregate_sequence"`
		Data              struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(factPayload, &fact); err != nil || fact.AggregateSequence != 1 || fact.Data.UserID != userID {
		t.Fatalf("failure fact=%s err=%v", factPayload, err)
	}

	input := domain.AdminRecoveryInput{
		CredentialID: credentialID, OperationID: "66000000-0000-4000-8000-000000000010", IdempotencyKey: "recovery-integration-0001",
		RequestSHA256: strings.Repeat("a", 64), ActionID: "66000000-0000-4000-8000-000000000011", CorrelationID: "66000000-0000-4000-8000-000000000012",
	}
	result, err := store.RecoverProvisioning(ctx, input)
	if err != nil || result.Replay || result.DesiredRevision != 2 || result.Status != domain.StatusProvisioning {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	replay, err := store.RecoverProvisioning(ctx, input)
	if err != nil || !replay.Replay || replay.OperationID != input.OperationID {
		t.Fatalf("recovery replay=%+v err=%v", replay, err)
	}
	collision := input
	collision.RequestSHA256 = strings.Repeat("b", 64)
	if _, err := store.RecoverProvisioning(ctx, collision); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("recovery collision=%v", err)
	}
	assertCount(t, store, "admin_recovery_requests", 1)
}

func integrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ACCESS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACCESS_TEST_DATABASE_URL is not set")
	}
	store, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	truncate := func(ctx context.Context) error {
		_, err := store.pool.Exec(ctx, `TRUNCATE payment_access_sli, admin_recovery_requests, security_audit_events, outbox, consumer_dead_letters, inbox, provisioning_outcome_state, subscription_lifecycle_state, url_idempotency, subscription_tokens, access_operations, access_assignment_snapshots, access_endpoint_snapshots, access_credentials CASCADE`)
		return err
	}
	if err := truncate(context.Background()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := truncate(context.Background()); err != nil {
			t.Errorf("clean access integration state: %v", err)
		}
		store.Close()
	})
	return store
}

func integrationMeta(eventID, eventType, aggregateID string, offset, aggregateSequence int64) domain.EventMeta {
	payloadHash := sha256.Sum256([]byte(eventID + "\n" + eventType))
	return domain.EventMeta{EventID: eventID, EventType: eventType, AggregateID: aggregateID, AggregateSequence: aggregateSequence, CorrelationID: "90000000-0000-4000-8000-000000000001", SourceTopic: eventType, SourcePartition: 0, SourceOffset: offset, PayloadSHA256: hex.EncodeToString(payloadHash[:])}
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
