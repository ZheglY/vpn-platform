package application

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

func TestReconcilerRestoresMissingActiveCredential(t *testing.T) {
	allocation := reconciliationAllocation("present")
	store := &reconciliationStore{candidates: []domain.ReconciliationCandidate{{Allocation: allocation}}}
	agent := &reconciliationAgent{actual: domain.CredentialActualState{CredentialID: allocation.CredentialID, State: "absent", ConfigRevision: 8}}
	access := &reconciliationAccess{material: domain.CredentialMaterial{CredentialID: allocation.CredentialID, SubscriptionID: "66000000-0000-4000-8000-000000000001", Revision: allocation.DesiredRevision, Protocol: "vless_reality", VLESSClientUUID: "64000000-0000-4000-8000-000000000001"}}
	worker := NewReconciliationWorker(store, access, agent, zap.NewNop(), 1, 10)
	worker.reconcile(context.Background())
	if agent.applies != 1 || agent.last.State != "present" || agent.last.VLESSClientUUID == "" || store.applied != 1 {
		t.Fatalf("reconciliation apply = %+v, count=%d, persisted=%d", agent.last, agent.applies, store.applied)
	}
}

func TestReconcilerRemovesCredentialWithoutFetchingSecret(t *testing.T) {
	allocation := reconciliationAllocation("absent")
	store := &reconciliationStore{candidates: []domain.ReconciliationCandidate{{Allocation: allocation}}}
	agent := &reconciliationAgent{actual: domain.CredentialActualState{CredentialID: allocation.CredentialID, DesiredRevision: allocation.DesiredRevision - 1, State: "present", ConfigRevision: 9}}
	access := &reconciliationAccess{}
	worker := NewReconciliationWorker(store, access, agent, zap.NewNop(), 1, 10)
	worker.reconcile(context.Background())
	if access.calls != 0 || agent.applies != 1 || agent.last.State != "absent" || agent.last.VLESSClientUUID != "" || store.revoked != 1 {
		t.Fatalf("revoke reconciliation fetched or retained secret: access_calls=%d desired=%+v persisted=%d", access.calls, agent.last, store.revoked)
	}
}

func TestReconcilerDoesNotOverwriteNewerActualRevision(t *testing.T) {
	allocation := reconciliationAllocation("absent")
	store := &reconciliationStore{candidates: []domain.ReconciliationCandidate{{Allocation: allocation}}}
	agent := &reconciliationAgent{actual: domain.CredentialActualState{CredentialID: allocation.CredentialID, DesiredRevision: allocation.DesiredRevision + 1, State: "present", ConfigRevision: 10}}
	worker := NewReconciliationWorker(store, &reconciliationAccess{}, agent, zap.NewNop(), 1, 10)
	worker.reconcile(context.Background())
	if agent.applies != 0 {
		t.Fatal("reconciler overwrote a newer node revision")
	}
}

func reconciliationAllocation(state string) domain.Allocation {
	return domain.Allocation{ID: "69000000-0000-4000-8000-000000000001", CredentialID: "62000000-0000-4000-8000-000000000001", DesiredOperationID: "63000000-0000-4000-8000-000000000001", Role: "primary", Protocol: "vless_reality", DesiredRevision: 2, DesiredState: state, Node: domain.Node{ID: "61000000-0000-4000-8000-000000000001"}}
}

type reconciliationStore struct {
	domain.Store
	candidates []domain.ReconciliationCandidate
	applied    int
	revoked    int
}

func (s *reconciliationStore) ListReconciliationCandidates(context.Context, int) ([]domain.ReconciliationCandidate, error) {
	return s.candidates, nil
}

func (s *reconciliationStore) MarkAllocationApplied(context.Context, string, string, int64) error {
	s.applied++
	return nil
}

func (s *reconciliationStore) MarkAllocationRevoked(context.Context, string, string, int64) error {
	s.revoked++
	return nil
}

type reconciliationAccess struct {
	material domain.CredentialMaterial
	calls    int
}

func (a *reconciliationAccess) GetCredentialMaterial(context.Context, string) (domain.CredentialMaterial, error) {
	a.calls++
	return a.material, nil
}

type reconciliationAgent struct {
	actual  domain.CredentialActualState
	last    domain.AgentDesiredState
	applies int
}

func (a *reconciliationAgent) Apply(_ context.Context, _ domain.Node, desired domain.AgentDesiredState) (domain.AgentResult, error) {
	a.applies++
	a.last = desired
	return domain.AgentResult{OperationID: desired.OperationID, CredentialID: desired.CredentialID, DesiredRevision: desired.DesiredRevision, State: desired.State, ConfigRevision: 11, AppliedAt: time.Now().UTC()}, nil
}
func (*reconciliationAgent) Status(context.Context, domain.Node) (domain.AgentStatus, error) {
	return domain.AgentStatus{}, nil
}
func (a *reconciliationAgent) CredentialState(context.Context, domain.Node, string) (domain.CredentialActualState, error) {
	return a.actual, nil
}
