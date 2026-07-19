package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
)

const (
	defaultOutDir = "secrets/dev-mtls"
	trustDomain   = "vpn-service"
	namespace     = "local"
	validFor      = 30 * 24 * time.Hour
)

type certSpec struct {
	name       string
	commonName string
	dnsNames   []string
	ipAddrs    []net.IP
	spiffeName string
	extUsages  []x509.ExtKeyUsage
}

func main() {
	outDir := defaultOutDir
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := run(outDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated local development mTLS material under %s\n", outDir)
}

func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return fmt.Errorf("chmod output directory: %w", err)
	}

	caCert, caKey, caPEM, caKeyPEM, err := newCA()
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "ca.crt"), caPEM, 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "ca.key"), caKeyPEM, 0o600); err != nil {
		return err
	}

	specs := []certSpec{
		{
			name:       "identity-service",
			commonName: "identity-service.local",
			dnsNames:   []string{"identity-service", "identity-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		},
		{
			name:       "telegram-bot",
			commonName: "ignored",
			spiffeName: "telegram-bot",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "identity-health",
			commonName: "ignored",
			spiffeName: "identity-health",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "catalog-service",
			commonName: "catalog-service.local",
			dnsNames:   []string{"catalog-service", "catalog-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		},
		{
			name:       "billing-service",
			commonName: "billing-service.local",
			dnsNames:   []string{"billing-service", "billing-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "billing-service",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "subscription-service",
			commonName: "subscription-service.local",
			dnsNames:   []string{"subscription-service", "subscription-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "subscription-service",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "access-service",
			commonName: "access-service.local",
			dnsNames:   []string{"access-service", "access-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "access-service",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "provisioning-service",
			commonName: "provisioning-service.local",
			dnsNames:   []string{"provisioning-service", "provisioning-service.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "provisioning-service",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "node-agent-primary",
			commonName: "node-agent-primary.local",
			dnsNames:   []string{"node-agent-primary", "node-agent-primary.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "node-agent-primary",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		},
		{
			name:       "node-agent-failover",
			commonName: "node-agent-failover.local",
			dnsNames:   []string{"node-agent-failover", "node-agent-failover.local", "localhost"},
			ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
			spiffeName: "node-agent-failover",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		},
		{
			name:       "node-health-primary",
			commonName: "ignored",
			spiffeName: "node-health-primary",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "node-health-failover",
			commonName: "ignored",
			spiffeName: "node-health-failover",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		},
		{
			name:       "admin-cli",
			commonName: "ignored",
			spiffeName: "admin-cli",
			extUsages:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		},
	}

	for _, spec := range specs {
		certPEM, keyPEM, err := newLeaf(spec, caCert, caKey)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(outDir, spec.name+".crt"), certPEM, 0o644); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(outDir, spec.name+".key"), keyPEM, 0o600); err != nil {
			return err
		}
	}

	return nil
}

func newCA() (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serialNumber(),
		Subject:               pkix.Name{CommonName: "vpn-service-dev-ca"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(validFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal CA key: %w", err)
	}
	return tmpl, key, pemBlock("CERTIFICATE", der), pemBlock("PRIVATE KEY", keyDER), nil
}

func newLeaf(spec certSpec, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate %s key: %w", spec.name, err)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject:      pkix.Name{CommonName: spec.commonName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  spec.extUsages,
		DNSNames:     spec.dnsNames,
		IPAddresses:  spec.ipAddrs,
	}
	if spec.spiffeName != "" {
		spiffeID, err := httpauth.SPIFFEID(trustDomain, namespace, spec.spiffeName)
		if err != nil {
			return nil, nil, err
		}
		uri, err := url.Parse(spiffeID)
		if err != nil {
			return nil, nil, fmt.Errorf("parse SPIFFE ID: %w", err)
		}
		tmpl.URIs = []*url.URL{uri}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s certificate: %w", spec.name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal %s key: %w", spec.name, err)
	}
	return pemBlock("CERTIFICATE", der), pemBlock("PRIVATE KEY", keyDER), nil
}

func serialNumber() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return serial
}

func pemBlock(blockType string, bytes []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes})
}

func writeFile(path string, contents []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, contents, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
