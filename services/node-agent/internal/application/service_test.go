package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

const (
	testNodeID       = "61000000-0000-4000-8000-000000000001"
	testCredentialID = "62000000-0000-4000-8000-000000000001"
	testOperationID  = "63000000-0000-4000-8000-000000000001"
	testClientID     = "64000000-0000-4000-8000-000000000001"
)

func TestApplyIsIdempotentAndRejectsOperationCollision(t *testing.T) {
	store := newMemoryStore(testNodeID)
	manager := &fakeManager{}
	service, err := NewService(context.Background(), testNodeID, "test", store, manager)
	if err != nil {
		t.Fatal(err)
	}
	desired := presentDesired(1, testOperationID)
	first, err := service.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || manager.applies != 1 {
		t.Fatalf("idempotent replay changed result or reapplied: first=%+v second=%+v applies=%d", first, second, manager.applies)
	}
	journal, err := json.Marshal(store.journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(journal), testClientID) || strings.Contains(string(journal), "vless_client_uuid") {
		t.Fatal("operation journal retained VLESS credential material")
	}
	desired.VLESSClientUUID = "64000000-0000-4000-8000-000000000002"
	if _, err := service.Apply(context.Background(), desired); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("collision error = %v, want ErrConflict", err)
	}
}

func TestApplyRejectsStaleRevisionAndPersistsAbsentTombstone(t *testing.T) {
	store := newMemoryStore(testNodeID)
	manager := &fakeManager{}
	service, err := NewService(context.Background(), testNodeID, "test", store, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), presentDesired(2, testOperationID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), presentDesired(1, "63000000-0000-4000-8000-000000000002")); !errors.Is(err, domain.ErrStale) {
		t.Fatalf("stale error = %v, want ErrStale", err)
	}
	absent := domain.DesiredState{OperationID: "63000000-0000-4000-8000-000000000003", CredentialID: testCredentialID, DesiredRevision: 3, State: "absent"}
	if _, err := service.Apply(context.Background(), absent); err != nil {
		t.Fatal(err)
	}
	actual, err := service.CredentialState(testCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.State != "absent" || actual.DesiredRevision != 3 {
		t.Fatalf("actual state = %+v", actual)
	}
	credential := store.snapshot.Credentials[testCredentialID]
	if credential.VLESSClientUUID != "" || credential.Protocol != "" {
		t.Fatal("absent tombstone retained credential material")
	}
}

func TestApplyReportsSnapshotAndRollbackFailures(t *testing.T) {
	persistErr := errors.New("snapshot unavailable")
	rollbackErr := errors.New("rollback unavailable")
	store := newMemoryStore(testNodeID)
	store.snapshotErr = persistErr
	manager := &fakeManager{applyErrors: map[int]error{2: rollbackErr}}
	service, err := NewService(context.Background(), testNodeID, "test", store, manager)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Apply(context.Background(), presentDesired(1, testOperationID))
	if !errors.Is(err, persistErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("apply error = %v, want snapshot and rollback failures", err)
	}
	if manager.applies != 2 {
		t.Fatalf("manager applies = %d, want candidate and rollback", manager.applies)
	}
}

func presentDesired(revision int, operationID string) domain.DesiredState {
	return domain.DesiredState{OperationID: operationID, CredentialID: testCredentialID, DesiredRevision: revision, State: "present", Protocol: "vless_reality", VLESSClientUUID: testClientID}
}

type memoryStore struct {
	snapshot    domain.Snapshot
	journal     domain.Journal
	snapshotErr error
}

func newMemoryStore(nodeID string) *memoryStore {
	return &memoryStore{snapshot: domain.Snapshot{NodeID: nodeID, Credentials: map[string]domain.Credential{}}, journal: domain.Journal{Entries: map[string]domain.JournalEntry{}}}
}

func (s *memoryStore) Load(context.Context, string) (domain.Snapshot, domain.Journal, error) {
	return cloneSnapshot(s.snapshot), s.journal, nil
}
func (s *memoryStore) SaveSnapshot(_ context.Context, snapshot domain.Snapshot) error {
	if s.snapshotErr != nil {
		return s.snapshotErr
	}
	s.snapshot = cloneSnapshot(snapshot)
	return nil
}
func (s *memoryStore) SaveJournal(_ context.Context, journal domain.Journal) error {
	s.journal = journal
	return nil
}

type fakeManager struct {
	applies     int
	applyErrors map[int]error
}

func (*fakeManager) Start(context.Context, domain.Snapshot) error { return nil }
func (m *fakeManager) Apply(context.Context, domain.Snapshot) error {
	m.applies++
	return m.applyErrors[m.applies]
}
func (*fakeManager) Healthy() bool                  { return true }
func (*fakeManager) Version(context.Context) string { return "Xray test" }
func (*fakeManager) Close(context.Context) error    { return nil }
