package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunWritesClientOnlyObservabilityCertificate(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "dev-mtls")
	if err := run(outDir); err != nil {
		t.Fatalf("run: %v", err)
	}

	certificatePEM, err := os.ReadFile(filepath.Join(outDir, "observability.crt"))
	if err != nil {
		t.Fatalf("read observability certificate: %v", err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("decode observability certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse observability certificate: %v", err)
	}
	if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("observability EKU = %v, want client auth only", certificate.ExtKeyUsage)
	}
	if len(certificate.URIs) != 1 || certificate.URIs[0].String() != "spiffe://vpn-service/ns/local/sa/observability" {
		t.Fatalf("observability URI = %v, want dedicated SPIFFE identity", certificate.URIs)
	}
}

func TestRunWritesTelemetryCertificateUsages(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "dev-mtls")
	if err := run(outDir); err != nil {
		t.Fatalf("run: %v", err)
	}

	tests := []struct {
		name       string
		wantClient bool
		wantServer bool
		wantDNS    string
	}{
		{name: "identity-service-otel", wantClient: true},
		{name: "catalog-service-otel", wantClient: true},
		{name: "billing-service-otel", wantClient: true},
		{name: "subscription-service-otel", wantClient: true},
		{name: "access-service-otel", wantClient: true},
		{name: "provisioning-service-otel", wantClient: true},
		{name: "node-agent-primary-otel", wantClient: true},
		{name: "node-agent-failover-otel", wantClient: true},
		{name: "notification-service-otel", wantClient: true},
		{name: "admin-service-otel", wantClient: true},
		{name: "telegram-bot-otel", wantClient: true},
		{name: "otel-collector", wantServer: true, wantDNS: "otel-collector"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificate := readCertificate(t, filepath.Join(outDir, test.name+".crt"))
			if containsUsage(certificate.ExtKeyUsage, x509.ExtKeyUsageClientAuth) != test.wantClient {
				t.Fatalf("client auth EKU = %v, want %v", certificate.ExtKeyUsage, test.wantClient)
			}
			if containsUsage(certificate.ExtKeyUsage, x509.ExtKeyUsageServerAuth) != test.wantServer {
				t.Fatalf("server auth EKU = %v, want %v", certificate.ExtKeyUsage, test.wantServer)
			}
			if test.wantDNS != "" && !containsString(certificate.DNSNames, test.wantDNS) {
				t.Fatalf("DNS names = %v, want %q", certificate.DNSNames, test.wantDNS)
			}
			if test.wantClient && (len(certificate.URIs) != 1 || certificate.URIs[0].String() != "spiffe://vpn-service/ns/local/sa/"+test.name) {
				t.Fatalf("SPIFFE URI = %v, want dedicated telemetry identity", certificate.URIs)
			}
			if test.wantClient {
				serverCertificate := readCertificate(t, filepath.Join(outDir, strings.TrimSuffix(test.name, "-otel")+".crt"))
				if bytes.Equal(certificate.RawSubjectPublicKeyInfo, serverCertificate.RawSubjectPublicKeyInfo) {
					t.Fatal("telemetry certificate reuses the service server key")
				}
			}
		})
	}
}

func TestRunWritesPrivateKeysWithOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file mode bits do not provide a useful private-key permission assertion")
	}
	outDir := filepath.Join(t.TempDir(), "dev-mtls")

	if err := run(outDir); err != nil {
		t.Fatalf("run: %v", err)
	}

	assertMode(t, outDir, 0o700)
	assertMode(t, filepath.Join(outDir, "ca.crt"), 0o644)
	assertMode(t, filepath.Join(outDir, "ca.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "identity-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "telegram-bot.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "identity-health.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "observability.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "catalog-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "billing-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "subscription-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "access-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "provisioning-service.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "node-agent-primary.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "node-agent-failover.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "node-health-primary.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "node-health-failover.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "admin-cli.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "otel-collector.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "identity-service-otel.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "node-agent-primary-otel.key"), 0o600)
	assertMode(t, filepath.Join(outDir, "telegram-bot-otel.key"), 0o600)
}

func readCertificate(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	certificatePEM, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("decode certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate
}

func containsUsage(usages []x509.ExtKeyUsage, want x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}
