package observability

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMTLSHandlerAllowsOnlyObservabilityIdentity(t *testing.T) {
	handler := MTLSHandler(NewRegistry(), "vpn-service", "local")

	allowed := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	allowed.TLS = verifiedServiceTLS(t, "observability")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf("observability status = %d, want %d", allowedResponse.Code, http.StatusOK)
	}

	denied := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	denied.TLS = verifiedServiceTLS(t, "identity-service")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("unrelated service status = %d, want %d", deniedResponse.Code, http.StatusForbidden)
	}
}

func verifiedServiceTLS(t *testing.T, service string) *tls.ConnectionState {
	t.Helper()
	identity, err := url.Parse("spiffe://vpn-service/ns/local/sa/" + service)
	if err != nil {
		t.Fatalf("parse test SPIFFE identity: %v", err)
	}
	certificate := &x509.Certificate{URIs: []*url.URL{identity}}
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
		VerifiedChains:   [][]*x509.Certificate{{certificate}},
	}
}
