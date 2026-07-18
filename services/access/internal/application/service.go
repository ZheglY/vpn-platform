package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/access/internal/credential"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
	"github.com/ZheglY/vpn-platform/services/access/internal/happ"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Service struct {
	store     domain.Store
	keyring   *credential.Keyring
	hasher    *credential.TokenHasher
	publicURL string
}

type Profile struct {
	Body      string
	ExpiresAt time.Time
}

type ProvisioningMaterial struct {
	CredentialID    string `json:"credential_id"`
	Revision        int    `json:"revision"`
	Protocol        string `json:"protocol"`
	VLESSClientUUID string `json:"vless_client_uuid"`
}

func NewService(store domain.Store, keyring *credential.Keyring, hasher *credential.TokenHasher, publicBaseURL string) (*Service, error) {
	parsed, err := url.Parse(publicBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("public subscription base URL is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &Service{store: store, keyring: keyring, hasher: hasher, publicURL: strings.TrimRight(parsed.String(), "/")}, nil
}

func (s *Service) ProcessEvent(ctx context.Context, meta domain.EventMeta, data json.RawMessage) error {
	switch meta.EventType {
	case "subscription.activated.v1", "subscription.extended.v1":
		var event domain.PeriodEvent
		if err := strictDecode(data, &event); err != nil {
			return ContractError("invalid_event_data", err)
		}
		if event.SubscriptionID != meta.AggregateID || meta.PartitionKey != "user:"+event.UserID || event.UserID == "" || !event.GraceEndsAt.After(event.PeriodStart) || event.GraceEndsAt.Before(event.PeriodEnd) {
			return ContractError("event_invariant_failed", domain.ErrDurableStateConflict)
		}
		credentialID, operationID, ciphertext, keyVersion, err := s.newCredential()
		if err != nil {
			return err
		}
		return s.store.ApplyPeriod(ctx, meta, event, domain.CredentialSeed{CredentialID: credentialID, OperationID: operationID, Ciphertext: ciphertext, KeyVersion: keyVersion})
	case "subscription.expired.v1", "subscription.revoked.v1":
		var event domain.TerminalEvent
		if err := strictDecode(data, &event); err != nil {
			return ContractError("invalid_event_data", err)
		}
		if event.SubscriptionID != meta.AggregateID || meta.PartitionKey != "user:"+event.UserID || event.UserID == "" || event.EffectiveAt.IsZero() {
			return ContractError("event_invariant_failed", domain.ErrDurableStateConflict)
		}
		operationID, err := cryptoutil.RandomUUID()
		if err != nil {
			return err
		}
		return s.store.ApplyTerminal(ctx, meta, event, operationID)
	case "access.provision.succeeded.v1":
		var event domain.ProvisionSucceeded
		if err := strictDecode(data, &event); err != nil {
			return ContractError("invalid_event_data", err)
		}
		if err := validateProvisionSucceeded(meta, event); err != nil {
			return ContractError("event_invariant_failed", err)
		}
		return s.store.ApplyProvisionSucceeded(ctx, meta, event)
	case "access.provision.failed.v1", "access.revoke.failed.v1":
		var event domain.OperationFailed
		if err := strictDecode(data, &event); err != nil {
			return ContractError("invalid_event_data", err)
		}
		if event.CredentialID != meta.AggregateID || !validUUIDs(event.OperationID, event.CredentialID) || event.FailedRevision < 1 || event.ReasonCode == "" || len(event.ReasonCode) > 64 || event.FailedAt.IsZero() || !validFailureScope(event.FailureScope) || !validUniqueUUIDs(event.PendingNodeIDs) {
			return ContractError("event_invariant_failed", domain.ErrDurableStateConflict)
		}
		kind := "provision"
		if meta.EventType == "access.revoke.failed.v1" {
			kind = "revoke"
		}
		return s.store.ApplyOperationFailed(ctx, meta, event, kind)
	case "access.revoke.succeeded.v1":
		var event domain.RevokeSucceeded
		if err := strictDecode(data, &event); err != nil {
			return ContractError("invalid_event_data", err)
		}
		if event.CredentialID != meta.AggregateID || !validUUIDs(event.OperationID, event.CredentialID) || event.DesiredRevision < 1 || event.AllocationRevision < 0 || !event.AllAssignedNodesRemoved || event.RevokedAt.IsZero() || !validUniqueUUIDs(event.NodeIDs) {
			return ContractError("event_invariant_failed", domain.ErrDurableStateConflict)
		}
		return s.store.ApplyRevokeSucceeded(ctx, meta, event)
	default:
		return ContractError("unsupported_event_type", fmt.Errorf("unsupported event type"))
	}
}

func (s *Service) IssueSubscriptionURL(ctx context.Context, subscriptionID, idempotencyKey, operation string) (string, error) {
	if operation != "issue" && operation != "rotate" {
		return "", fmt.Errorf("unsupported URL operation")
	}
	token, err := cryptoutil.RandomBase64URL(32)
	if err != nil {
		return "", err
	}
	tokenID, err := cryptoutil.RandomUUID()
	if err != nil {
		return "", err
	}
	requestDigest := sha256.Sum256([]byte(operation + "\n" + subscriptionID))
	seed := domain.TokenSeed{
		TokenID: tokenID, LookupHMAC: s.hasher.Sum(token), IdempotencyKey: idempotencyKey,
		RequestSHA256: hex.EncodeToString(requestDigest[:]),
	}
	if err := s.store.IssueToken(ctx, subscriptionID, operation, seed); err != nil {
		return "", err
	}
	return s.publicURL + "/s/" + token, nil
}

func (s *Service) GetAccessStatus(ctx context.Context, subscriptionID string) (domain.AccessStatus, error) {
	return s.store.GetAccessStatus(ctx, subscriptionID)
}

func (s *Service) GetProfile(ctx context.Context, token string) (Profile, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return Profile{}, domain.ErrNotFound
	}
	record, err := s.store.GetProfileByTokenHMAC(ctx, s.hasher.Sum(token))
	if err != nil {
		return Profile{}, err
	}
	vlessUUID, err := s.keyring.Decrypt(record.KeyVersion, record.Ciphertext, record.CredentialID)
	if err != nil {
		return Profile{}, fmt.Errorf("decrypt profile credential: %w", err)
	}
	body, err := happ.Render(vlessUUID, record.Endpoints)
	if err != nil {
		return Profile{}, fmt.Errorf("render Happ profile: %w", err)
	}
	return Profile{Body: body, ExpiresAt: record.EntitlementExpiresAt}, nil
}

func (s *Service) GetProvisioningMaterial(ctx context.Context, credentialID, actorService string) (ProvisioningMaterial, error) {
	if actorService == "" || len(actorService) > 64 {
		return ProvisioningMaterial{}, fmt.Errorf("provisioning actor identity is invalid")
	}
	record, err := s.store.GetProvisioningRecord(ctx, credentialID)
	if err != nil {
		return ProvisioningMaterial{}, err
	}
	vlessUUID, err := s.keyring.Decrypt(record.KeyVersion, record.Ciphertext, record.CredentialID)
	if err != nil {
		return ProvisioningMaterial{}, fmt.Errorf("decrypt provisioning credential: %w", err)
	}
	if err := s.store.RecordCredentialMaterialAccess(ctx, record.CredentialID, actorService); err != nil {
		return ProvisioningMaterial{}, err
	}
	return ProvisioningMaterial{CredentialID: record.CredentialID, Revision: record.Revision, Protocol: "vless_reality", VLESSClientUUID: vlessUUID}, nil
}

func (s *Service) newCredential() (string, string, []byte, int, error) {
	credentialID, err := cryptoutil.RandomUUID()
	if err != nil {
		return "", "", nil, 0, err
	}
	operationID, err := cryptoutil.RandomUUID()
	if err != nil {
		return "", "", nil, 0, err
	}
	vlessUUID, err := cryptoutil.RandomUUID()
	if err != nil {
		return "", "", nil, 0, err
	}
	nonce, err := cryptoutil.RandomBytes(s.keyring.NonceSize())
	if err != nil {
		return "", "", nil, 0, err
	}
	ciphertext, keyVersion, err := s.keyring.Encrypt(vlessUUID, nonce, credentialID)
	return credentialID, operationID, ciphertext, keyVersion, err
}

func validateProvisionSucceeded(meta domain.EventMeta, event domain.ProvisionSucceeded) error {
	if event.CredentialID != meta.AggregateID || !validUUIDs(event.OperationID, event.CredentialID) || event.AppliedRevision < 1 || event.AppliedAt.IsZero() || (event.Status != domain.StatusActive && event.Status != domain.StatusDegraded) {
		return domain.ErrDurableStateConflict
	}
	primary, failover := 0, 0
	seen := make(map[string]struct{}, len(event.Endpoints))
	for _, endpoint := range event.Endpoints {
		if !validUUIDs(endpoint.NodeID) || happ.ValidateEndpoint(endpoint) != nil {
			return domain.ErrDurableStateConflict
		}
		if _, ok := seen[endpoint.NodeID]; ok {
			return domain.ErrDurableStateConflict
		}
		seen[endpoint.NodeID] = struct{}{}
		switch endpoint.Role {
		case "primary":
			primary++
		case "failover":
			failover++
		default:
			return domain.ErrDurableStateConflict
		}
	}
	if primary != 1 || failover > 1 || (event.Status == domain.StatusActive && failover != 1) || (event.Status == domain.StatusDegraded && failover != 0) {
		return domain.ErrDurableStateConflict
	}
	return nil
}

func validUUIDs(values ...string) bool {
	for _, value := range values {
		if !uuidPattern.MatchString(value) {
			return false
		}
	}
	return true
}

func validUniqueUUIDs(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validUUIDs(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validFailureScope(value string) bool {
	return value == "primary" || value == "failover" || value == "all" || value == "unknown"
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

type contractError struct {
	code string
	err  error
}

func (e *contractError) Error() string { return e.code }
func (e *contractError) Unwrap() error { return e.err }

func ContractError(code string, err error) error { return &contractError{code: code, err: err} }

func ContractErrorCode(err error) (string, bool) {
	var target *contractError
	if errors.As(err, &target) {
		return target.code, true
	}
	if errors.Is(err, domain.ErrDurableStateConflict) {
		return "durable_state_conflict", true
	}
	return "", false
}
