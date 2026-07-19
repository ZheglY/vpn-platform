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

func TestIntegrationOperationClaimWaitsForEarlierCredentialCommand(t *testing.T) {
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
	if err != nil || !ok || claimed.ID != first.OperationID {
		t.Fatalf("first claim = %+v, %t, %v", claimed, ok, err)
	}
	if err := store.RetryOperation(ctx, claimed.ID, "test_retry", time.Hour); err != nil {
		t.Fatal(err)
	}
	if blocked, ok, err := store.ClaimOperation(ctx, 30*time.Second); err != nil || ok {
		t.Fatalf("later command bypassed retrying predecessor: %+v, %t, %v", blocked, ok, err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE operations SET state='failed',completed_at=clock_timestamp() WHERE id=$1`, first.OperationID); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = store.ClaimOperation(ctx, 30*time.Second)
	if err != nil || !ok || claimed.ID != second.OperationID {
		t.Fatalf("second claim after predecessor completion = %+v, %t, %v", claimed, ok, err)
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
		_, err := store.pool.Exec(context.Background(), `TRUNCATE outbox,consumer_dead_letters,node_health_snapshots,allocations,operations,command_inbox,credential_command_cursors,nodes CASCADE`)
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
