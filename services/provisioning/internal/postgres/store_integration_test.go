package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

func TestIntegrationCommandOrderingReplayAndSanitizedDeadLetter(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	credentialID := "62000000-0000-4000-8000-000000000001"
	first := domain.Command{OperationID: "63000000-0000-4000-8000-000000000001", CredentialID: credentialID, DesiredRevision: 1}
	second := domain.Command{OperationID: "63000000-0000-4000-8000-000000000002", CredentialID: credentialID, DesiredRevision: 2}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000002", "access.revoke.request.v1", credentialID, 2, 2), second, "revoke", 5); !errors.Is(err, domain.ErrSequenceGap) {
		t.Fatalf("gap error = %v, want ErrSequenceGap", err)
	}
	firstMeta := commandMeta("65000000-0000-4000-8000-000000000001", "access.provision.request.v1", credentialID, 1, 1)
	if err := store.RecordCommand(ctx, firstMeta, first, "provision", 5); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommand(ctx, firstMeta, first, "provision", 5); err != nil {
		t.Fatalf("exact replay failed: %v", err)
	}
	collision := firstMeta
	collision.PayloadSHA256 = strings.Repeat("b", 64)
	if err := store.RecordCommand(ctx, collision, first, "provision", 5); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("collision error = %v, want ErrConflict", err)
	}
	reusedOperation := second
	reusedOperation.OperationID = first.OperationID
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000003", "access.revoke.request.v1", credentialID, 3, 2), reusedOperation, "revoke", 5); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reused operation error = %v, want ErrConflict", err)
	}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000002", "access.revoke.request.v1", credentialID, 2, 2), second, "revoke", 5); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, store, "command_inbox", 2)
	assertTableCount(t, store, "operations", 2)

	secretMarker := "64000000-0000-4000-8000-000000000099"
	if err := store.RecordDeadLetter(ctx, "access.provision.request.v1", 0, 9, strings.Repeat("c", 64), "invalid_event_data"); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := store.pool.QueryRow(ctx, `SELECT payload::text FROM outbox WHERE topic='access.provision.request.v1.dlq'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, secretMarker) || strings.Contains(payload, "vless_client_uuid") || !strings.Contains(payload, strings.Repeat("c", 64)) {
		t.Fatalf("DLQ payload is not sanitized: %s", payload)
	}
}

func TestIntegrationConcurrentPlacementRespectsReservedCapacity(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seeds := testNodeSeeds(2)
	if err := store.SeedNodes(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	credentials := []string{"62000000-0000-4000-8000-000000000011", "62000000-0000-4000-8000-000000000012"}
	for index, credentialID := range credentials {
		operationID := []string{"63000000-0000-4000-8000-000000000011", "63000000-0000-4000-8000-000000000012"}[index]
		meta := commandMeta([]string{"65000000-0000-4000-8000-000000000011", "65000000-0000-4000-8000-000000000012"}[index], "access.provision.request.v1", credentialID, int64(index+10), 1)
		if err := store.RecordCommand(ctx, meta, domain.Command{OperationID: operationID, CredentialID: credentialID, DesiredRevision: 1}, "provision", 5); err != nil {
			t.Fatal(err)
		}
	}
	operations := make([]domain.Operation, 0, 2)
	for range credentials {
		operation, ok, err := store.ClaimOperation(ctx, 30*time.Second)
		if err != nil || !ok {
			t.Fatalf("claim operation: %+v, %t, %v", operation, ok, err)
		}
		operations = append(operations, operation)
	}
	placement := domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000001", PeriodID: "67000000-0000-4000-8000-000000000001", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wait sync.WaitGroup
	for _, operation := range operations {
		wait.Add(1)
		go func(operation domain.Operation) {
			defer wait.Done()
			<-start
			allocations, err := store.EnsureAllocations(ctx, operation, placement, "vless_reality")
			if err == nil && (len(allocations) != 2 || allocations[0].Node.ID == allocations[1].Node.ID) {
				err = errors.New("allocation did not select two distinct nodes")
			}
			results <- err
		}(operation)
	}
	close(start)
	wait.Wait()
	close(results)
	var success, capacity int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, domain.ErrCapacity):
			capacity++
		default:
			t.Fatalf("unexpected placement error: %v", err)
		}
	}
	if success != 1 || capacity != 1 {
		t.Fatalf("placement outcomes success=%d capacity=%d", success, capacity)
	}
	var maximumAllocated int
	if err := store.pool.QueryRow(ctx, `SELECT max(allocated_clients) FROM nodes`).Scan(&maximumAllocated); err != nil {
		t.Fatal(err)
	}
	if maximumAllocated != 1 {
		t.Fatalf("allocated_clients max = %d, want 1 at 80%% threshold for capacity 2", maximumAllocated)
	}
}

func TestIntegrationHigherRevisionSupersedesUnstartedCredentialCommand(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	credentialID := "62000000-0000-4000-8000-000000000021"
	first := domain.Command{OperationID: "63000000-0000-4000-8000-000000000021", CredentialID: credentialID, DesiredRevision: 1}
	second := domain.Command{OperationID: "63000000-0000-4000-8000-000000000022", CredentialID: credentialID, DesiredRevision: 2}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000021", "access.provision.request.v1", credentialID, 21, 1), first, "provision", 5); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000022", "access.revoke.request.v1", credentialID, 22, 2), second, "revoke", 5); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := store.ClaimOperation(ctx, 30*time.Second)
	if err != nil || !ok || claimed.ID != second.OperationID {
		t.Fatalf("current generation claim = %+v, %t, %v", claimed, ok, err)
	}
	var firstState string
	if err := store.pool.QueryRow(ctx, `SELECT state FROM operations WHERE id=$1`, first.OperationID).Scan(&firstState); err != nil || firstState != "superseded" {
		t.Fatalf("older operation state = %q, %v", firstState, err)
	}
	if err := store.RetryOperation(ctx, first.OperationID, "stale_worker", time.Hour); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("superseded operation retry = %v", err)
	}
}

func TestIntegrationProvisionAndRevokeResultsAreTransactionalAndSecretFree(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seeds := testNodeSeeds(10)
	if err := store.SeedNodes(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	credentialID := "62000000-0000-4000-8000-000000000031"
	provisionCommand := domain.Command{OperationID: "63000000-0000-4000-8000-000000000031", CredentialID: credentialID, DesiredRevision: 1}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000031", "access.provision.request.v1", credentialID, 31, 1), provisionCommand, "provision", 5); err != nil {
		t.Fatal(err)
	}
	provision, ok, err := store.ClaimOperation(ctx, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim provision: %+v, %t, %v", provision, ok, err)
	}
	placement := domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000031", PeriodID: "67000000-0000-4000-8000-000000000031", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}
	allocations, err := store.EnsureAllocations(ctx, provision, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationApplied(ctx, provision.ID, allocation.Node.ID, 2); err != nil {
			t.Fatal(err)
		}
	}
	allocations, err = store.ListAllocations(ctx, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteProvision(ctx, provision, "active", allocations); err != nil {
		t.Fatal(err)
	}
	provisionPayload := outboxPayload(t, store, "access.provision.succeeded.v1")
	if strings.Contains(string(provisionPayload), "vless_client_uuid") || strings.Contains(string(provisionPayload), "64000000-0000-4000-8000-000000000031") {
		t.Fatal("provisioning result outbox retained credential material")
	}

	revokeCommand := domain.Command{OperationID: "63000000-0000-4000-8000-000000000032", CredentialID: credentialID, DesiredRevision: 2}
	if err := store.RecordCommand(ctx, commandMeta("65000000-0000-4000-8000-000000000032", "access.revoke.request.v1", credentialID, 32, 2), revokeCommand, "revoke", 5); err != nil {
		t.Fatal(err)
	}
	revoke, ok, err := store.ClaimOperation(ctx, 30*time.Second)
	if err != nil || !ok || revoke.ID != revokeCommand.OperationID {
		t.Fatalf("claim revoke: %+v, %t, %v", revoke, ok, err)
	}
	if err := store.PrepareRevoke(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	allocations, err = store.ListAllocations(ctx, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationRevoked(ctx, revoke.ID, allocation.Node.ID, 3); err != nil {
			t.Fatal(err)
		}
	}
	allocations, err = store.ListAllocations(ctx, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRevoke(ctx, revoke, allocations); err != nil {
		t.Fatal(err)
	}
	var envelope platformkafka.Envelope
	if err := json.Unmarshal(outboxPayload(t, store, "access.revoke.succeeded.v1"), &envelope); err != nil {
		t.Fatal(err)
	}
	var result struct {
		AllocationRevision      int      `json:"allocation_revision"`
		AllAssignedNodesRemoved bool     `json:"all_assigned_nodes_removed"`
		NodeIDs                 []string `json:"node_ids"`
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.AllocationRevision != 1 || !result.AllAssignedNodesRemoved || len(result.NodeIDs) != 2 || result.NodeIDs[0] == result.NodeIDs[1] {
		t.Fatalf("invalid revoke proof: %+v", result)
	}
	var allocated int
	if err := store.pool.QueryRow(ctx, `SELECT sum(allocated_clients) FROM nodes`).Scan(&allocated); err != nil || allocated != 0 {
		t.Fatalf("capacity after revoke = %d, %v", allocated, err)
	}
}

func TestIntegrationHigherRevisionRebindsRevokedAllocationGeneration(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seeds := testNodeSeeds(10)
	if err := store.SeedNodes(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	credentialID := "62000000-0000-4000-8000-000000000041"
	provision := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000041", "65000000-0000-4000-8000-000000000041", "access.provision.request.v1", "provision", 1, 41)
	placement := domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000041", PeriodID: "67000000-0000-4000-8000-000000000041", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}
	allocations, err := store.EnsureAllocations(ctx, provision, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationApplied(ctx, provision.ID, allocation.Node.ID, 2); err != nil {
			t.Fatal(err)
		}
	}
	allocations, _ = store.ListAllocations(ctx, credentialID)
	if err := store.CompleteProvision(ctx, provision, "active", allocations); err != nil {
		t.Fatal(err)
	}

	revoke := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000042", "65000000-0000-4000-8000-000000000042", "access.revoke.request.v1", "revoke", 2, 42)
	if err := store.PrepareRevoke(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationRevoked(ctx, revoke.ID, allocation.Node.ID, 3); err != nil {
			t.Fatal(err)
		}
	}
	allocations, _ = store.ListAllocations(ctx, credentialID)
	if err := store.CompleteRevoke(ctx, revoke, allocations); err != nil {
		t.Fatal(err)
	}

	reactivation := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000043", "65000000-0000-4000-8000-000000000043", "access.provision.request.v1", "provision", 3, 43)
	allocations, err = store.EnsureAllocations(ctx, reactivation, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if allocation.DesiredOperationID != reactivation.ID || allocation.DesiredRevision != 3 || allocation.DesiredState != "present" || allocation.State != "pending" {
			t.Fatalf("allocation was not rebound to higher revision: %+v", allocation)
		}
		if err := store.MarkAllocationApplied(ctx, reactivation.ID, allocation.Node.ID, 4); err != nil {
			t.Fatalf("higher-revision allocation apply failed: %v", err)
		}
	}
	var reserved int
	if err := store.pool.QueryRow(ctx, `SELECT sum(allocated_clients) FROM nodes`).Scan(&reserved); err != nil || reserved != 2 {
		t.Fatalf("reactivation capacity = %d, %v; want exactly two reservations", reserved, err)
	}
}

func TestIntegrationTerminalFailureRecoversAtHigherRevisionAndPublishesInOrder(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seeds := testNodeSeeds(10)
	if err := store.SeedNodes(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	credentialID := "62000000-0000-4000-8000-000000000061"
	placement := domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000061", PeriodID: "67000000-0000-4000-8000-000000000061", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}
	failed := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000061", "65000000-0000-4000-8000-000000000061", "access.provision.request.v1", "provision", 1, 61)
	allocations, err := store.EnsureAllocations(ctx, failed, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationFailed(ctx, failed.ID, allocation.Node.ID, "primary_apply_failed"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CompleteProvisionFailure(ctx, failed, "primary", "primary_apply_failed", []string{allocations[0].Node.ID}); err != nil {
		t.Fatal(err)
	}

	recovery := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000062", "65000000-0000-4000-8000-000000000062", "access.provision.request.v1", "provision", 2, 62)
	allocations, err = store.EnsureAllocations(ctx, recovery, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationApplied(ctx, recovery.ID, allocation.Node.ID, 2); err != nil {
			t.Fatal(err)
		}
	}
	allocations, _ = store.ListAllocations(ctx, credentialID)
	if err := store.CompleteProvision(ctx, recovery, "active", allocations); err != nil {
		t.Fatal(err)
	}
	var reserved int
	if err := store.pool.QueryRow(ctx, `SELECT sum(allocated_clients) FROM nodes`).Scan(&reserved); err != nil || reserved != 2 {
		t.Fatalf("recovery capacity = %d, %v", reserved, err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE outbox SET created_at=clock_timestamp()-aggregate_sequence*interval '1 hour' WHERE aggregate_id=$1`, credentialID); err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.ClaimOutbox(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim first outcome: %+v %t %v", first, ok, err)
	}
	var firstEnvelope platformkafka.Envelope
	if err := json.Unmarshal(first.Payload, &firstEnvelope); err != nil || firstEnvelope.AggregateSequence != 1 || firstEnvelope.EventType != "access.provision.failed.v1" {
		t.Fatalf("first outcome = %+v, %v", firstEnvelope, err)
	}
	if blocked, ok, err := store.ClaimOutbox(ctx, time.Minute); err != nil || ok {
		t.Fatalf("later outcome bypassed unpublished predecessor: %+v %t %v", blocked, ok, err)
	}
	if err := store.CompleteOutbox(ctx, first.EventID); err != nil {
		t.Fatal(err)
	}
	second, ok, err := store.ClaimOutbox(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim recovery outcome: %+v %t %v", second, ok, err)
	}
	var secondEnvelope platformkafka.Envelope
	if err := json.Unmarshal(second.Payload, &secondEnvelope); err != nil || secondEnvelope.AggregateSequence != 2 || secondEnvelope.EventType != "access.provision.succeeded.v1" {
		t.Fatalf("second outcome = %+v, %v", secondEnvelope, err)
	}
}

func TestIntegrationReactivationSupersedesPartiallyAppliedRevoke(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seeds := testNodeSeeds(10)
	if err := store.SeedNodes(ctx, seeds); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	credentialID := "62000000-0000-4000-8000-000000000071"
	placement := domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000071", PeriodID: "67000000-0000-4000-8000-000000000071", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}
	provision := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000071", "65000000-0000-4000-8000-000000000071", "access.provision.request.v1", "provision", 1, 71)
	allocations, err := store.EnsureAllocations(ctx, provision, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationApplied(ctx, provision.ID, allocation.Node.ID, 2); err != nil {
			t.Fatal(err)
		}
	}
	allocations, _ = store.ListAllocations(ctx, credentialID)
	if err := store.CompleteProvision(ctx, provision, "active", allocations); err != nil {
		t.Fatal(err)
	}

	revoke := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000072", "65000000-0000-4000-8000-000000000072", "access.revoke.request.v1", "revoke", 2, 72)
	if err := store.PrepareRevoke(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAllocationRevoked(ctx, revoke.ID, allocations[0].Node.ID, 3); err != nil {
		t.Fatal(err)
	}
	reactivation := recordAndClaimOperation(t, store, credentialID, "63000000-0000-4000-8000-000000000073", "65000000-0000-4000-8000-000000000073", "access.provision.request.v1", "provision", 3, 73)
	if err := store.MarkAllocationRevoked(ctx, revoke.ID, allocations[1].Node.ID, 4); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale revoke acknowledgement = %v", err)
	}
	allocations, err = store.EnsureAllocations(ctx, reactivation, placement, "vless_reality")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if err := store.MarkAllocationApplied(ctx, reactivation.ID, allocation.Node.ID, 5); err != nil {
			t.Fatal(err)
		}
	}
	allocations, _ = store.ListAllocations(ctx, credentialID)
	if err := store.CompleteProvision(ctx, reactivation, "active", allocations); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRevoke(ctx, revoke, allocations); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("superseded revoke completion = %v", err)
	}
	var reserved int
	if err := store.pool.QueryRow(ctx, `SELECT sum(allocated_clients) FROM nodes`).Scan(&reserved); err != nil || reserved != 2 {
		t.Fatalf("reactivation capacity = %d, %v", reserved, err)
	}
}

func TestIntegrationReconciliationClaimDoesNotStarveBeyondBatch(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	seed := testNodeSeeds(10)[0]
	if err := store.SeedNodes(ctx, []domain.NodeSeed{seed}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNodeHealth(ctx, domain.AgentStatus{NodeID: seed.ID, ConfigRevision: 1, XrayHealthy: true, AgentVersion: "test", XrayVersion: "Xray 26.3.27"}, 45*time.Second); err != nil {
		t.Fatal(err)
	}
	fixtures := [][3]string{
		{"62000000-0000-4000-8000-000000000051", "63000000-0000-4000-8000-000000000051", "69000000-0000-4000-8000-000000000051"},
		{"62000000-0000-4000-8000-000000000052", "63000000-0000-4000-8000-000000000052", "69000000-0000-4000-8000-000000000052"},
		{"62000000-0000-4000-8000-000000000053", "63000000-0000-4000-8000-000000000053", "69000000-0000-4000-8000-000000000053"},
	}
	for index, fixture := range fixtures {
		if _, err := store.pool.Exec(ctx, `INSERT INTO operations (id,credential_id,kind,desired_revision,command_sequence,state,correlation_id,causation_event_id,completed_at) VALUES ($1,$2,'provision',1,$3,'succeeded',$4,$5,clock_timestamp())`, fixture[1], fixture[0], index+1, "68000000-0000-4000-8000-000000000051", fixture[1]); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO allocations (id,credential_id,operation_id,desired_operation_id,node_id,role,protocol,desired_revision,desired_state,allocation_revision,state,applied_config_revision) VALUES ($1,$2,$3,$3,$4,'primary','vless_reality',1,'present',1,'active',1)`, fixture[2], fixture[0], fixture[1], seed.ID); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]struct{}{}
	for range 2 {
		candidates, err := store.ClaimReconciliationCandidates(ctx, 2, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			seen[candidate.Allocation.ID] = struct{}{}
		}
	}
	if len(seen) != len(fixtures) {
		t.Fatalf("reconciliation starved allocations beyond first batch: saw %d of %d", len(seen), len(fixtures))
	}
}

func recordAndClaimOperation(t *testing.T, store *Store, credentialID, operationID, eventID, eventType, kind string, revision int, offset int64) domain.Operation {
	t.Helper()
	command := domain.Command{OperationID: operationID, CredentialID: credentialID, DesiredRevision: revision}
	if err := store.RecordCommand(context.Background(), commandMeta(eventID, eventType, credentialID, offset, int64(revision)), command, kind, 5); err != nil {
		t.Fatal(err)
	}
	operation, ok, err := store.ClaimOperation(context.Background(), 30*time.Second)
	if err != nil || !ok || operation.ID != operationID {
		t.Fatalf("claim operation %s: %+v, %t, %v", operationID, operation, ok, err)
	}
	return operation
}

func integrationStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("PROVISIONING_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROVISIONING_TEST_DATABASE_URL is not set")
	}
	store, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	truncate := func() error {
		_, err := store.pool.Exec(context.Background(), `TRUNCATE outbox,consumer_dead_letters,node_health_snapshots,allocations,operations,command_inbox,credential_outcome_cursors,credential_command_cursors,nodes CASCADE`)
		return err
	}
	if err := truncate(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := truncate(); err != nil {
			t.Errorf("clean provisioning integration state: %v", err)
		}
		store.Close()
	})
	return store
}

func commandMeta(eventID, eventType, credentialID string, offset, sequence int64) domain.EventMeta {
	return domain.EventMeta{EventID: eventID, EventType: eventType, AggregateID: credentialID, AggregateSequence: sequence, PartitionKey: "credential:" + credentialID, CorrelationID: "68000000-0000-4000-8000-000000000001", OccurredAt: time.Now().UTC(), SourceTopic: eventType, SourcePartition: 0, SourceOffset: offset, PayloadSHA256: strings.Repeat("a", 64)}
}

func testNodeSeeds(capacity int) []domain.NodeSeed {
	return []domain.NodeSeed{
		{ID: "61000000-0000-4000-8000-000000000001", Region: "ru-test", ManagementURL: "https://node-agent-primary:8443", ManagementSPIFFEID: "spiffe://vpn-service/ns/local/sa/node-agent-primary", CapacityLimit: capacity, ReservePercent: 20, PublicAddress: "127.0.0.1", PublicPort: 1443, ServerName: "camouflage.local", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0123456789abcdef", SpiderX: "/", Label: "Primary"},
		{ID: "61000000-0000-4000-8000-000000000002", Region: "ru-test", ManagementURL: "https://node-agent-failover:8443", ManagementSPIFFEID: "spiffe://vpn-service/ns/local/sa/node-agent-failover", CapacityLimit: capacity, ReservePercent: 20, PublicAddress: "127.0.0.1", PublicPort: 2443, ServerName: "camouflage.local", RealityPublicKey: strings.Repeat("B", 43), ShortID: "fedcba9876543210", SpiderX: "/", Label: "Failover"},
	}
}

func assertTableCount(t *testing.T, store *Store, table string, want int) {
	t.Helper()
	var count int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func outboxPayload(t *testing.T, store *Store, topic string) []byte {
	t.Helper()
	var payload []byte
	if err := store.pool.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE topic=$1`, topic).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
