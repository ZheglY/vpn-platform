package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ZheglY/vpn-platform/services/admin/internal/application"
	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
)

func TestProtectRejectsServiceCertificate(t *testing.T) {
	handler := New(&authService{}, nil, "vpn-service", "local")
	protected := handler.Protect("identity.read", func(w http.ResponseWriter, _ *http.Request, _ domain.Principal) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.TLS = verifiedState("spiffe://vpn-service/ns/local/sa/admin-cli")
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestDecodeStrictRejectsOversizedMutationBody(t *testing.T) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeStrict(strings.NewReader(strings.Repeat(" ", maxMutationBodyBytes+1)), &request); err == nil {
		t.Fatal("oversized mutation body accepted")
	}
}

func TestProtectRejectsMissingVerifiedCertificateAsUnauthenticated(t *testing.T) {
	handler := New(&authService{}, nil, "vpn-service", "local")
	protected := handler.Protect("identity.read", func(w http.ResponseWriter, _ *http.Request, _ domain.Principal) { w.WriteHeader(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestProtectAppliesPermissionAfterCertificate(t *testing.T) {
	handler := New(&authService{deny: true}, nil, "vpn-service", "local")
	protected := handler.Protect("subscription.revoke", func(w http.ResponseWriter, _ *http.Request, _ domain.Principal) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.TLS = verifiedState("spiffe://vpn-service/ns/local/admin/support")
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestDecodeMutationRejectsSecretReason(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"reason":"leaked vless://secret"}`))
	request.SetPathValue("credential_id", "11111111-1111-4111-8111-111111111111")
	recorder := httptest.NewRecorder()
	_, _, _, ok := decodeMutation(recorder, request, false)
	if ok || recorder.Code != http.StatusBadRequest {
		t.Fatalf("ok=%v status=%d", ok, recorder.Code)
	}
}

func TestExecuteReportsRecoverableOwnerOutcome(t *testing.T) {
	service := &authService{
		action: domain.Action{
			ActionID:      "11111111-1111-4111-8111-111111111111",
			Action:        "subscription.revoke",
			TargetType:    "subscription",
			TargetID:      "22222222-2222-4222-8222-222222222222",
			Status:        "outcome_unknown",
			CorrelationID: "33333333-3333-4333-8333-333333333333",
		},
		executeErr: errors.New("response lost"),
	}
	handler := New(service, nil, "vpn-service", "local")
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Idempotency-Key", "stage7-handler-test")
	recorder := httptest.NewRecorder()

	handler.execute(
		recorder,
		request,
		domain.Principal{Permissions: []string{"subscription.revoke"}},
		"subscription.revoke",
		"subscription.revoke",
		"subscription",
		service.action.TargetID,
		"support request",
		"support",
		nil,
	)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Action    domain.Action `json:"action"`
		ErrorCode string        `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != "owner_outcome_unknown" || body.Action.Status != "outcome_unknown" {
		t.Fatalf("body=%+v", body)
	}
}

type authService struct {
	deny       bool
	action     domain.Action
	executeErr error
}

func (s *authService) Authenticate(context.Context, string, string) (domain.Principal, error) {
	return domain.Principal{Permissions: []string{"identity.read"}}, nil
}
func (s *authService) Authorize(domain.Principal, string) error {
	if s.deny {
		return domain.ErrForbidden
	}
	return nil
}
func (s *authService) Execute(context.Context, application.ExecuteInput, application.OwnerCall) (domain.Action, error) {
	return s.action, s.executeErr
}
func (s *authService) ListAudit(context.Context, domain.Principal, int) ([]domain.AuditEvent, error) {
	return nil, nil
}

func verifiedState(raw string) *tls.ConnectionState {
	uri, _ := url.Parse(raw)
	certificate := &x509.Certificate{URIs: []*url.URL{uri}}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}}
}
