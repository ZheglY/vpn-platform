package httpauth

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
)

func TestRequireServiceAllowsMatchingVerifiedSPIFFEIdentity(t *testing.T) {
	handler := httpserver.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := FromContext(r.Context())
		if !ok {
			t.Fatal("identity missing from context")
		}
		if identity.TrustDomain != "vpn-service" || identity.Namespace != "local" || identity.Name != "telegram-bot" {
			t.Fatalf("identity = %+v", identity)
		}
		w.WriteHeader(http.StatusNoContent)
	}), httpserver.RequestID, RequireService(ServicePolicy{
		TrustDomain: "vpn-service",
		Namespace:   "local",
		Allowed:     []string{"telegram-bot"},
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = verifiedTLSState("spiffe://vpn-service/ns/local/sa/telegram-bot")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequireServiceRejectsMissingCertificate(t *testing.T) {
	handler := protectedNoopHandler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireServiceRejectsUnverifiedPeerCertificate(t *testing.T) {
	handler := protectedNoopHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = peerOnlyTLSState("spiffe://vpn-service/ns/local/sa/telegram-bot")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireServiceRejectsWrongIdentity(t *testing.T) {
	handler := protectedNoopHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = verifiedTLSState("spiffe://vpn-service/ns/local/sa/billing-service")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestIdentityFromTLSRejectsForeignTrustDomain(t *testing.T) {
	if _, ok := IdentityFromTLS(verifiedTLSState("spiffe://evil.example/ns/local/sa/telegram-bot"), "vpn-service", "local"); ok {
		t.Fatal("foreign trust domain accepted")
	}
}

func TestIdentityFromTLSRejectsInvalidSPIFFEPath(t *testing.T) {
	cases := []string{
		"spiffe://vpn-service/local/telegram-bot",
		"spiffe://vpn-service/ns/local/service/telegram-bot",
		"spiffe://vpn-service/ns/local/sa/telegram-bot/extra",
		"spiffe://vpn-service/ns/prod/sa/telegram-bot",
	}

	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			if _, ok := IdentityFromTLS(verifiedTLSState(tc), "vpn-service", "local"); ok {
				t.Fatal("invalid SPIFFE path accepted")
			}
		})
	}
}

func TestIdentityFromTLSRejectsDNSAndCommonNameFallback(t *testing.T) {
	cert := &x509.Certificate{
		Subject:  pkix.Name{CommonName: "telegram-bot"},
		DNSNames: []string{"telegram-bot"},
	}

	if _, ok := IdentityFromTLS(&tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}}, "vpn-service", "local"); ok {
		t.Fatal("DNS/CN fallback accepted")
	}
}

func TestSPIFFEID(t *testing.T) {
	id, err := SPIFFEID("vpn-service", "local", "telegram-bot")
	if err != nil {
		t.Fatalf("SPIFFEID returned error: %v", err)
	}
	if id != "spiffe://vpn-service/ns/local/sa/telegram-bot" {
		t.Fatalf("id = %q", id)
	}
}

func protectedNoopHandler() http.Handler {
	return httpserver.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpserver.RequestID, RequireService(ServicePolicy{
		TrustDomain: "vpn-service",
		Namespace:   "local",
		Allowed:     []string{"telegram-bot"},
	}))
}

func verifiedTLSState(spiffeID string) *tls.ConnectionState {
	cert := certificateWithURI(spiffeID)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificateWithURI("spiffe://evil.example/ns/local/sa/attacker")},
		VerifiedChains:   [][]*x509.Certificate{{cert}},
	}
}

func peerOnlyTLSState(spiffeID string) *tls.ConnectionState {
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificateWithURI(spiffeID)},
	}
}

func certificateWithURI(rawURI string) *x509.Certificate {
	uri, err := url.Parse(rawURI)
	if err != nil {
		panic(err)
	}
	return &x509.Certificate{
		Subject: pkix.Name{CommonName: "ignored"},
		URIs:    []*url.URL{uri},
	}
}
