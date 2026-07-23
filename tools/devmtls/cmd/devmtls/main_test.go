package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
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
