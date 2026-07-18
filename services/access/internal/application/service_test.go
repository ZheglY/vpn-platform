package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/access/internal/credential"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestIssueSubscriptionURLPersistsOnlyLookupHMAC(t *testing.T) {
	store := &fakeStore{}
	service, hasher := newTestService(t, store)
	issued, err := service.IssueSubscriptionURL(context.Background(), "018f0e61-bca5-7a40-a06f-e4c0f53128ad", "request-key-0001", "issue")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(issued, "https://subscriptions.example/s/")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("issued token does not contain 256 random bits: %v", err)
	}
	if bytes.Contains(store.tokenSeed.LookupHMAC, []byte(token)) || !bytes.Equal(store.tokenSeed.LookupHMAC, hasher.Sum(token)) {
		t.Fatal("store did not receive only the expected keyed lookup value")
	}
	if store.tokenSeed.IdempotencyKey != "request-key-0001" || store.tokenOperation != "issue" {
		t.Fatal("issuance metadata mismatch")
	}
}

func TestGetProfileDecryptsOnlyForRendering(t *testing.T) {
	store := &fakeStore{}
	service, _ := newTestService(t, store)
	nonce := bytes.Repeat([]byte{3}, 12)
	ciphertext, version, err := service.keyring.Encrypt("018f0e61-bca5-7a40-a06f-e4c0f53128ad", nonce, "018f0e61-bca5-7a40-a06f-e4c0f53128ae")
	if err != nil {
		t.Fatal(err)
	}
	store.profile = domain.ProfileRecord{
		CredentialID: "018f0e61-bca5-7a40-a06f-e4c0f53128ae", Ciphertext: ciphertext, KeyVersion: version,
		EntitlementExpiresAt: time.Unix(1790951622, 0).UTC(),
		Endpoints:            []domain.EndpointSnapshot{{NodeID: "018f0e61-bca5-7a40-a06f-e4c0f53128af", Role: "primary", Address: "vpn.example.com", Port: 443, ServerName: "cdn.example.com", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "VPN Primary"}},
	}
	profile, err := service.GetProfile(context.Background(), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(profile.Body, "vless://018f0e61-bca5-7a40-a06f-e4c0f53128ad@vpn.example.com:443?") || profile.ExpiresAt.Unix() != 1790951622 {
		t.Fatal("unexpected rendered profile")
	}
}

func TestProvisionSuccessRejectsReadyWithoutExpectedTopology(t *testing.T) {
	store := &fakeStore{}
	service, _ := newTestService(t, store)
	meta := domain.EventMeta{EventType: "access.provision.succeeded.v1", AggregateID: "018f0e61-bca5-7a40-a06f-e4c0f53128ad"}
	data := []byte(`{"operation_id":"018f0e61-bca5-7a40-a06f-e4c0f53128ae","credential_id":"018f0e61-bca5-7a40-a06f-e4c0f53128ad","applied_revision":1,"status":"active","endpoints":[{"node_id":"018f0e61-bca5-7a40-a06f-e4c0f53128af","role":"primary","address":"vpn.example.com","port":443,"server_name":"cdn.example.com","reality_public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","short_id":"0011","label":"Primary"}],"applied_at":"2026-07-18T12:00:00Z"}`)
	err := service.ProcessEvent(context.Background(), meta, data)
	if _, poison := ContractErrorCode(err); !poison || store.provisionApplied {
		t.Fatal("active result without failover was accepted")
	}
}

func newTestService(t *testing.T, store *fakeStore) (*Service, *credential.TokenHasher) {
	t.Helper()
	keyring, err := credential.NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := credential.NewTokenHasher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, keyring, hasher, "https://subscriptions.example")
	if err != nil {
		t.Fatal(err)
	}
	return service, hasher
}

type fakeStore struct {
	tokenSeed        domain.TokenSeed
	tokenOperation   string
	profile          domain.ProfileRecord
	provisionApplied bool
}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) ApplyPeriod(context.Context, domain.EventMeta, domain.PeriodEvent, domain.CredentialSeed) error {
	return nil
}
func (f *fakeStore) ApplyTerminal(context.Context, domain.EventMeta, domain.TerminalEvent, string) error {
	return nil
}
func (f *fakeStore) ApplyProvisionSucceeded(context.Context, domain.EventMeta, domain.ProvisionSucceeded) error {
	f.provisionApplied = true
	return nil
}
func (f *fakeStore) ApplyOperationFailed(context.Context, domain.EventMeta, domain.OperationFailed, string) error {
	return nil
}
func (f *fakeStore) ApplyRevokeSucceeded(context.Context, domain.EventMeta, domain.RevokeSucceeded) error {
	return nil
}
func (f *fakeStore) IssueToken(_ context.Context, _ string, operation string, seed domain.TokenSeed) error {
	f.tokenOperation, f.tokenSeed = operation, seed
	return nil
}
func (f *fakeStore) GetAccessStatus(context.Context, string) (domain.AccessStatus, error) {
	return domain.AccessStatus{}, nil
}
func (f *fakeStore) GetProfileByTokenHMAC(context.Context, []byte) (domain.ProfileRecord, error) {
	if len(f.profile.Ciphertext) == 0 {
		return domain.ProfileRecord{}, domain.ErrNotFound
	}
	return f.profile, nil
}
func (f *fakeStore) GetProvisioningRecord(context.Context, string) (domain.ProvisioningRecord, error) {
	return domain.ProvisioningRecord{}, domain.ErrNotFound
}
func (f *fakeStore) RecordDeadLetter(context.Context, string, int32, int64, string, string) error {
	return nil
}
func (f *fakeStore) ClaimOutbox(context.Context, time.Duration) (domain.OutboxMessage, bool, error) {
	return domain.OutboxMessage{}, false, nil
}
func (f *fakeStore) CompleteOutbox(context.Context, string) error             { return nil }
func (f *fakeStore) RetryOutbox(context.Context, string, time.Duration) error { return nil }

var _ domain.Store = (*fakeStore)(nil)
