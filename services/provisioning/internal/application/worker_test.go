package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

func TestProvisionCompletesDegradedAfterBoundedFailoverFailure(t *testing.T) {
	store := newOperationStore()
	agent := &operationAgent{failNodeID: store.allocations[1].Node.ID}
	worker := newTestOperationWorker(store, agent)

	if err := worker.provision(context.Background(), testOperation(5, 5)); err != nil {
		t.Fatal(err)
	}
	if store.provisionStatus != "degraded" || store.failureReason != "" || store.allocations[0].State != "active" || store.allocations[1].State != "failed" {
		t.Fatalf("unexpected degraded outcome: status=%q reason=%q allocations=%+v", store.provisionStatus, store.failureReason, store.allocations)
	}
}

func TestProvisionFailsTerminallyWhenPrimaryCannotBeApplied(t *testing.T) {
	store := newOperationStore()
	agent := &operationAgent{failNodeID: store.allocations[0].Node.ID}
	worker := newTestOperationWorker(store, agent)

	if err := worker.provision(context.Background(), testOperation(5, 5)); err != nil {
		t.Fatal(err)
	}
	if store.failureScope != "primary" || store.failureReason != "primary_apply_failed" || store.provisionStatus != "" {
		t.Fatalf("unexpected primary failure: scope=%q reason=%q status=%q", store.failureScope, store.failureReason, store.provisionStatus)
	}
}

func TestProvisionFailsTerminallyWhenCapacityIsExhausted(t *testing.T) {
	store := newOperationStore()
	store.ensureError = domain.ErrCapacity
	worker := newTestOperationWorker(store, &operationAgent{})

	if err := worker.provision(context.Background(), testOperation(3, 3)); err != nil {
		t.Fatal(err)
	}
	if store.failureScope != "all" || store.failureReason != "capacity_exhausted" {
		t.Fatalf("unexpected capacity failure: scope=%q reason=%q", store.failureScope, store.failureReason)
	}
}

func newTestOperationWorker(store *operationStore, agent *operationAgent) *OperationWorker {
	return NewOperationWorker(store, operationAccess{}, operationSubscription{}, agent, zap.NewNop(), time.Millisecond, time.Millisecond, time.Second)
}

func testOperation(attempts, maxAttempts int) domain.Operation {
	return domain.Operation{ID: "63000000-0000-4000-8000-000000000001", CredentialID: "62000000-0000-4000-8000-000000000001", Kind: "provision", DesiredRevision: 1, Attempts: attempts, MaxAttempts: maxAttempts}
}

type operationStore struct {
	domain.Store
	allocations     []domain.Allocation
	ensureError     error
	provisionStatus string
	failureScope    string
	failureReason   string
}

func newOperationStore() *operationStore {
	return &operationStore{allocations: []domain.Allocation{
		{ID: "69000000-0000-4000-8000-000000000001", CredentialID: "62000000-0000-4000-8000-000000000001", Role: "primary", State: "pending", Node: domain.Node{ID: "61000000-0000-4000-8000-000000000001"}},
		{ID: "69000000-0000-4000-8000-000000000002", CredentialID: "62000000-0000-4000-8000-000000000001", Role: "failover", State: "pending", Node: domain.Node{ID: "61000000-0000-4000-8000-000000000002"}},
	}}
}

func (s *operationStore) EnsureAllocations(context.Context, domain.Operation, domain.Placement, string) ([]domain.Allocation, error) {
	return append([]domain.Allocation(nil), s.allocations...), s.ensureError
}

func (s *operationStore) ListAllocations(context.Context, string) ([]domain.Allocation, error) {
	return append([]domain.Allocation(nil), s.allocations...), nil
}

func (s *operationStore) MarkAllocationApplied(_ context.Context, _ string, nodeID string, _ int64) error {
	for index := range s.allocations {
		if s.allocations[index].Node.ID == nodeID {
			s.allocations[index].State = "active"
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *operationStore) MarkAllocationFailed(_ context.Context, _ string, nodeID, _ string) error {
	for index := range s.allocations {
		if s.allocations[index].Node.ID == nodeID {
			s.allocations[index].State = "failed"
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *operationStore) CompleteProvision(_ context.Context, _ domain.Operation, status string, _ []domain.Allocation) error {
	s.provisionStatus = status
	return nil
}

func (s *operationStore) CompleteProvisionFailure(_ context.Context, _ domain.Operation, scope, reason string, _ []string) error {
	s.failureScope, s.failureReason = scope, reason
	return nil
}

type operationAccess struct{}

func (operationAccess) GetCredentialMaterial(context.Context, string) (domain.CredentialMaterial, error) {
	return domain.CredentialMaterial{CredentialID: "62000000-0000-4000-8000-000000000001", SubscriptionID: "66000000-0000-4000-8000-000000000001", Revision: 1, Protocol: "vless_reality", VLESSClientUUID: "64000000-0000-4000-8000-000000000001"}, nil
}

type operationSubscription struct{}

func (operationSubscription) GetPlacement(context.Context, string) (domain.Placement, error) {
	return domain.Placement{SubscriptionID: "66000000-0000-4000-8000-000000000001", PeriodID: "67000000-0000-4000-8000-000000000001", Region: "ru-test", PrimaryNodes: 1, FailoverNodes: 1, ValidUntil: time.Now().Add(time.Hour)}, nil
}

type operationAgent struct{ failNodeID string }

func (a *operationAgent) Apply(_ context.Context, node domain.Node, desired domain.AgentDesiredState) (domain.AgentResult, error) {
	if node.ID == a.failNodeID {
		return domain.AgentResult{}, errors.New("test agent failure")
	}
	return domain.AgentResult{OperationID: desired.OperationID, CredentialID: desired.CredentialID, DesiredRevision: desired.DesiredRevision, State: desired.State, ConfigRevision: 2, AppliedAt: time.Now().UTC()}, nil
}

func (*operationAgent) Status(context.Context, domain.Node) (domain.AgentStatus, error) {
	return domain.AgentStatus{}, nil
}

func (*operationAgent) CredentialState(context.Context, domain.Node, string) (domain.CredentialActualState, error) {
	return domain.CredentialActualState{}, nil
}
