package httpserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewMutualTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caPEM, caKey, caCert := newTestCA(t)
	certPEM, keyPEM := newTestServerCertificate(t, caCert, caKey)

	caPath := writeTestFile(t, dir, "ca.pem", caPEM)
	certPath := writeTestFile(t, dir, "server.pem", certPEM)
	keyPath := writeTestFile(t, dir, "server-key.pem", keyPEM)

	cfg, err := NewMutualTLSConfig(certPath, keyPath, []string{caPath})
	if err != nil {
		t.Fatalf("NewMutualTLSConfig returned error: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %d, want TLS 1.3", cfg.MinVersion)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates = %d, want 1", len(cfg.Certificates))
	}
	if cfg.ClientCAs == nil {
		t.Fatal("ClientCAs is nil")
	}
	if _, err := caCert.Verify(x509.VerifyOptions{Roots: cfg.ClientCAs}); err != nil {
		t.Fatalf("client CA pool does not verify the test CA: %v", err)
	}
}

func TestNewMutualTLSConfigRejectsMissingClientCA(t *testing.T) {
	if _, err := NewMutualTLSConfig("server.pem", "server-key.pem", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestMutualTLSRejectsExpiredClientCertificate(t *testing.T) {
	dir := t.TempDir()
	caPEM, caKey, caCert := newTestCA(t)
	serverPEM, serverKeyPEM := newTestServerCertificate(t, caCert, caKey)
	validClientPEM, validClientKeyPEM := newTestClientCertificate(
		t, caCert, caKey, time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	expiredClientPEM, expiredClientKeyPEM := newTestClientCertificate(
		t, caCert, caKey, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour),
	)

	caPath := writeTestFile(t, dir, "ca.pem", caPEM)
	serverPath := writeTestFile(t, dir, "server.pem", serverPEM)
	serverKeyPath := writeTestFile(t, dir, "server-key.pem", serverKeyPEM)
	serverConfig, err := NewMutualTLSConfig(serverPath, serverKeyPath, []string{caPath})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("append test root")
	}
	request := func(certPEM, keyPEM []byte) error {
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS13,
				RootCAs:      roots,
				ServerName:   "identity-service.local",
				Certificates: []tls.Certificate{certificate},
			}},
			Timeout: 2 * time.Second,
		}
		response, err := client.Get(server.URL)
		if response != nil {
			_ = response.Body.Close()
		}
		return err
	}
	if err := request(validClientPEM, validClientKeyPEM); err != nil {
		t.Fatalf("valid client certificate was rejected: %v", err)
	}
	if err := request(expiredClientPEM, expiredClientKeyPEM); err == nil {
		t.Fatal("expired client certificate completed a mutual TLS request")
	}
}

func newTestCA(t *testing.T) ([]byte, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	cert := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key, parsed
}

func newTestServerCertificate(t *testing.T, caCert *x509.Certificate, caKey *rsa.PrivateKey) ([]byte, []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "identity-service.local"},
		DNSNames:     []string{"identity-service.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
}

func newTestClientCertificate(
	t *testing.T,
	caCert *x509.Certificate,
	caKey *rsa.PrivateKey,
	notBefore time.Time,
	notAfter time.Time,
) ([]byte, []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(notAfter.UnixNano()),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client certificate: %v", err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
}

func writeTestFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
