package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Service struct {
	mu           sync.Mutex
	nodeID       string
	agentVersion string
	store        domain.StateStore
	xray         domain.XrayManager
	snapshot     domain.Snapshot
	journal      domain.Journal
}

func NewService(ctx context.Context, nodeID, agentVersion string, store domain.StateStore, manager domain.XrayManager) (*Service, error) {
	if !uuidPattern.MatchString(nodeID) || agentVersion == "" {
		return nil, fmt.Errorf("node identity and agent version are required")
	}
	snapshot, journal, err := store.Load(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := manager.Start(ctx, snapshot); err != nil {
		return nil, err
	}
	return &Service{nodeID: nodeID, agentVersion: agentVersion, store: store, xray: manager, snapshot: snapshot, journal: journal}, nil
}

func (s *Service) Apply(ctx context.Context, desired domain.DesiredState) (domain.ApplyResult, error) {
	if err := validateDesired(desired); err != nil {
		return domain.ApplyResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	requestHash, err := hashDesired(desired)
	if err != nil {
		return domain.ApplyResult{}, err
	}
	if existing, ok := s.journal.Entries[desired.OperationID]; ok {
		if existing.RequestHash != requestHash {
			return domain.ApplyResult{}, domain.ErrConflict
		}
		return existing.Result, nil
	}
	current, exists := s.snapshot.Credentials[desired.CredentialID]
	if exists && desired.DesiredRevision < current.DesiredRevision {
		return domain.ApplyResult{}, domain.ErrStale
	}
	if exists && desired.DesiredRevision == current.DesiredRevision {
		if !sameDesired(current, desired) {
			return domain.ApplyResult{}, domain.ErrConflict
		}
		result := newResult(desired, s.snapshot.ConfigRevision)
		if err := s.recordJournal(ctx, requestHash, result); err != nil {
			return domain.ApplyResult{}, err
		}
		return result, nil
	}
	previous := cloneSnapshot(s.snapshot)
	next := cloneSnapshot(s.snapshot)
	next.ConfigRevision++
	next.Credentials[desired.CredentialID] = domain.Credential{
		CredentialID: desired.CredentialID, DesiredRevision: desired.DesiredRevision, State: desired.State,
		Protocol: desired.Protocol, VLESSClientUUID: desired.VLESSClientUUID,
	}
	if desired.State == "absent" {
		credential := next.Credentials[desired.CredentialID]
		credential.Protocol, credential.VLESSClientUUID = "", ""
		next.Credentials[desired.CredentialID] = credential
	}
	if err := s.xray.Apply(ctx, next); err != nil {
		return domain.ApplyResult{}, err
	}
	if err := s.store.SaveSnapshot(ctx, next); err != nil {
		persistErr := fmt.Errorf("persist node desired state: %w", err)
		if rollbackErr := s.xray.Apply(context.Background(), previous); rollbackErr != nil {
			return domain.ApplyResult{}, errors.Join(persistErr, fmt.Errorf("restore previous node desired state: %w", rollbackErr))
		}
		return domain.ApplyResult{}, persistErr
	}
	s.snapshot = next
	result := newResult(desired, next.ConfigRevision)
	if err := s.recordJournal(ctx, requestHash, result); err != nil {
		return domain.ApplyResult{}, err
	}
	return result, nil
}

func (s *Service) Status(ctx context.Context) domain.Status {
	s.mu.Lock()
	active := 0
	for _, credential := range s.snapshot.Credentials {
		if credential.State == "present" {
			active++
		}
	}
	configRevision := s.snapshot.ConfigRevision
	xrayHealthy := s.xray.Healthy()
	s.mu.Unlock()
	return domain.Status{NodeID: s.nodeID, ConfigRevision: configRevision, ActiveClients: active, XrayHealthy: xrayHealthy, AgentVersion: s.agentVersion, XrayVersion: s.xray.Version(ctx)}
}

func (s *Service) CredentialState(credentialID string) (domain.CredentialState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.snapshot.Credentials[credentialID]
	if !ok {
		return domain.CredentialState{CredentialID: credentialID, State: "absent", ConfigRevision: s.snapshot.ConfigRevision}, nil
	}
	return domain.CredentialState{CredentialID: credentialID, DesiredRevision: credential.DesiredRevision, State: credential.State, ConfigRevision: s.snapshot.ConfigRevision}, nil
}

func (s *Service) Healthy() bool                   { return s.xray.Healthy() }
func (s *Service) Close(ctx context.Context) error { return s.xray.Close(ctx) }

func (s *Service) recordJournal(ctx context.Context, requestHash string, result domain.ApplyResult) error {
	entry := domain.JournalEntry{OperationID: result.OperationID, RequestHash: requestHash, Result: result, CompletedAt: result.AppliedAt}
	s.journal.Entries[result.OperationID] = entry
	if err := s.store.SaveJournal(ctx, s.journal); err != nil {
		delete(s.journal.Entries, result.OperationID)
		return fmt.Errorf("persist node operation journal: %w", err)
	}
	return nil
}

func hashDesired(desired domain.DesiredState) (string, error) {
	encoded, err := json.Marshal(desired)
	if err != nil {
		return "", fmt.Errorf("hash node desired state: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func newResult(desired domain.DesiredState, configRevision int64) domain.ApplyResult {
	return domain.ApplyResult{OperationID: desired.OperationID, CredentialID: desired.CredentialID, DesiredRevision: desired.DesiredRevision, State: desired.State, ConfigRevision: configRevision, AppliedAt: time.Now().UTC()}
}

func validateDesired(desired domain.DesiredState) error {
	if !uuidPattern.MatchString(desired.OperationID) || !uuidPattern.MatchString(desired.CredentialID) || desired.DesiredRevision < 1 || (desired.State != "present" && desired.State != "absent") {
		return domain.ErrConflict
	}
	if desired.State == "present" {
		if desired.Protocol != "vless_reality" || !uuidPattern.MatchString(desired.VLESSClientUUID) {
			return domain.ErrConflict
		}
	} else if desired.Protocol != "" || desired.VLESSClientUUID != "" {
		return domain.ErrConflict
	}
	return nil
}

func validateSnapshot(snapshot domain.Snapshot) error {
	if !uuidPattern.MatchString(snapshot.NodeID) || snapshot.ConfigRevision < 0 {
		return fmt.Errorf("node snapshot is invalid")
	}
	for credentialID, credential := range snapshot.Credentials {
		desired := domain.DesiredState{OperationID: "00000000-0000-4000-8000-000000000000", CredentialID: credentialID, DesiredRevision: credential.DesiredRevision, State: credential.State, Protocol: credential.Protocol, VLESSClientUUID: credential.VLESSClientUUID}
		if credential.CredentialID != credentialID || validateDesired(desired) != nil {
			return fmt.Errorf("node snapshot credential is invalid")
		}
	}
	return nil
}

func sameDesired(current domain.Credential, desired domain.DesiredState) bool {
	return current.State == desired.State && current.Protocol == desired.Protocol && current.VLESSClientUUID == desired.VLESSClientUUID
}

func cloneSnapshot(snapshot domain.Snapshot) domain.Snapshot {
	cloned := domain.Snapshot{NodeID: snapshot.NodeID, ConfigRevision: snapshot.ConfigRevision, Credentials: make(map[string]domain.Credential, len(snapshot.Credentials))}
	for key, credential := range snapshot.Credentials {
		cloned.Credentials[key] = credential
	}
	return cloned
}
