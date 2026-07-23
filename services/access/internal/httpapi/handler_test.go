package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
	"github.com/ZheglY/vpn-platform/services/access/internal/application"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestHappSubscriptionHeadersAndBody(t *testing.T) {
	service := &fakeService{profile: application.Profile{Body: "vless://profile\n", ExpiresAt: time.Unix(1790951622, 0)}}
	handler := New(service, "VPN Platform", "https://support.example", 6, nil)
	request := httptest.NewRequest(http.MethodGet, "/s/secret", nil)
	request.SetPathValue("token", "secret")
	response := httptest.NewRecorder()
	handler.GetHappSubscription(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "vless://profile\n" {
		t.Fatalf("unexpected response: %d %q", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "profile-title": "VPN Platform", "profile-update-interval": "6",
		"subscription-userinfo": "upload=0; download=0; total=0; expire=1790951622", "support-url": "https://support.example",
		"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s: want %q, got %q", name, want, got)
		}
	}
}

func TestDecodeStrictJSONRejectsOversizedAdminRecoveryBody(t *testing.T) {
	var request struct {
		ActionID string `json:"action_id"`
	}
	if err := decodeStrictJSON(strings.NewReader(strings.Repeat(" ", maxAdminRecoveryBodyBytes+1)), &request); err == nil {
		t.Fatal("oversized admin recovery body accepted")
	}
}

func TestUnavailableTokensAreIndistinguishable(t *testing.T) {
	handler := New(&fakeService{profileErr: domain.ErrNotFound}, "VPN", "", 6, nil)
	var baseline string
	for _, token := range []string{"malformed", strings.Repeat("a", 43), strings.Repeat("z", 256)} {
		request := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
		request.SetPathValue("token", token)
		response := httptest.NewRecorder()
		handler.GetHappSubscription(response, request)
		fingerprint := strings.Join([]string{response.Result().Status, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Body.String()}, "|")
		if baseline == "" {
			baseline = fingerprint
		}
		if fingerprint != baseline || response.Code != http.StatusNotFound {
			t.Fatalf("token responses differ: %q and %q", baseline, fingerprint)
		}
	}
}

func TestMalformedSubscriptionPathsUseGenericNoStoreResponse(t *testing.T) {
	handler := New(&fakeService{profileErr: domain.ErrNotFound}, "VPN", "", 6, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", handler.GetHappSubscription)
	mux.HandleFunc("GET /s/", handler.GetUnavailableHappSubscription)
	mux.HandleFunc("GET /s", handler.GetUnavailableHappSubscription)
	var baseline string
	for _, path := range []string{"/s/unknown", "/s", "/s/", "/s/a/b"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		fingerprint := strings.Join([]string{response.Result().Status, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Header().Get("Pragma"), response.Body.String()}, "|")
		if baseline == "" {
			baseline = fingerprint
		}
		if response.Code != http.StatusNotFound || fingerprint != baseline {
			t.Fatalf("malformed path %q differs: %q vs %q", path, fingerprint, baseline)
		}
	}
}

func TestRequestLoggingUsesRouteTemplateNotToken(t *testing.T) {
	secret := "do-not-log-this-subscription-token"
	var output bytes.Buffer
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	logger := zap.New(zapcore.NewCore(encoder, zapcore.AddSync(&output), zap.InfoLevel))
	handler := New(&fakeService{profileErr: domain.ErrNotFound}, "VPN", "", 6, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", handler.GetHappSubscription)
	wrapped := httpserver.Chain(mux, httpserver.RequestID, httpserver.LogRequests(logger))
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/s/"+secret, nil))
	logged := output.String()
	if strings.Contains(logged, secret) || !strings.Contains(logged, `"route":"GET /s/{token}"`) {
		t.Fatalf("unsafe request log: %s", logged)
	}
}

func TestHappSubscriptionRateLimitIsNoStore(t *testing.T) {
	handler := New(&fakeService{}, "VPN", "", 6, fakeLimiter{allowed: false})
	request := httptest.NewRequest(http.MethodGet, "/s/secret", nil)
	request.SetPathValue("token", "secret")
	response := httptest.NewRecorder()
	handler.GetHappSubscription(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("unexpected rate limit response: %d %#v", response.Code, response.Header())
	}
}

type fakeService struct {
	profile    application.Profile
	profileErr error
}

type fakeLimiter struct {
	allowed bool
	err     error
}

func (f fakeLimiter) Allow(context.Context, string, string) (bool, error) {
	return f.allowed, f.err
}

func (f *fakeService) IssueSubscriptionURL(context.Context, string, string, string) (string, error) {
	return "", errors.New("not implemented")
}
func (f *fakeService) GetAccessStatus(context.Context, string) (domain.AccessStatus, error) {
	return domain.AccessStatus{}, errors.New("not implemented")
}
func (f *fakeService) RecoverProvisioning(context.Context, domain.AdminRecoveryInput) (domain.AdminRecoveryResult, error) {
	return domain.AdminRecoveryResult{}, errors.New("not implemented")
}
func (f *fakeService) GetProfile(context.Context, string) (application.Profile, error) {
	return f.profile, f.profileErr
}
func (f *fakeService) GetProvisioningMaterial(context.Context, string, string) (application.ProvisioningMaterial, error) {
	return application.ProvisioningMaterial{}, errors.New("not implemented")
}
