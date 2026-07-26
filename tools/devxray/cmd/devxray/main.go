package main

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
)

const defaultOutDir = "secrets/dev-xray"

type nodeSeed struct {
	ID                 string `json:"id"`
	Region             string `json:"region"`
	ManagementURL      string `json:"management_url"`
	ManagementSPIFFEID string `json:"management_spiffe_id"`
	CapacityLimit      int    `json:"capacity_limit"`
	ReservePercent     int    `json:"reserve_percent"`
	PublicAddress      string `json:"public_address"`
	PublicPort         int    `json:"public_port"`
	ServerName         string `json:"server_name"`
	RealityPublicKey   string `json:"reality_public_key"`
	ShortID            string `json:"short_id"`
	SpiderX            string `json:"spider_x"`
	Label              string `json:"label"`
}

func main() {
	outDir := defaultOutDir
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := run(outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Generated local development Xray material under %s\n", outDir)
}

func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("create Xray secret directory: %w", err)
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return fmt.Errorf("protect Xray secret directory: %w", err)
	}
	certificate, key, err := camouflageCertificate()
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "camouflage.crt"), certificate, 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "camouflage.key"), key, 0o600); err != nil {
		return err
	}
	type definition struct {
		name, id, managementURL, spiffeID, shortID, label string
		publicPort                                        int
	}
	definitions := []definition{
		{name: "primary", id: "61000000-0000-4000-8000-000000000001", managementURL: "https://node-agent-primary:8443", spiffeID: "spiffe://vpn-service/ns/local/sa/node-agent-primary", shortID: "0123456789abcdef", label: "Local primary", publicPort: 1443},
		{name: "failover", id: "61000000-0000-4000-8000-000000000002", managementURL: "https://node-agent-failover:8443", spiffeID: "spiffe://vpn-service/ns/local/sa/node-agent-failover", shortID: "fedcba9876543210", label: "Local failover", publicPort: 2443},
	}
	seeds := make([]nodeSeed, 0, len(definitions))
	publicKeys := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate %s REALITY key: %w", definition.name, err)
		}
		privateEncoded := base64.RawURLEncoding.EncodeToString(privateKey.Bytes())
		publicEncoded := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
		publicKeys[definition.name] = publicEncoded
		if err := writeFile(filepath.Join(outDir, definition.name+".key"), []byte(privateEncoded+"\n"), 0o600); err != nil {
			return err
		}
		seeds = append(seeds, nodeSeed{ID: definition.id, Region: "ru-test", ManagementURL: definition.managementURL, ManagementSPIFFEID: definition.spiffeID, CapacityLimit: 10, ReservePercent: 20, PublicAddress: "127.0.0.1", PublicPort: definition.publicPort, ServerName: "camouflage.local", RealityPublicKey: publicEncoded, ShortID: definition.shortID, SpiderX: "/", Label: definition.label})
	}
	contents, err := json.MarshalIndent(seeds, "", "  ")
	if err != nil {
		return fmt.Errorf("encode node seed: %w", err)
	}
	if err := writeFile(filepath.Join(outDir, "nodes.json"), append(contents, '\n'), 0o600); err != nil {
		return err
	}
	return writeSmokeFixtures(outDir, publicKeys)
}

func writeSmokeFixtures(outDir string, publicKeys map[string]string) error {
	clientID, err := cryptoutil.RandomUUID()
	if err != nil {
		return fmt.Errorf("generate smoke VLESS UUID: %w", err)
	}
	const credentialID = "62000000-0000-4000-8000-000000000090"
	present := map[string]any{"operation_id": "63000000-0000-4000-8000-000000000090", "credential_id": credentialID, "desired_revision": 1, "state": "present", "protocol": "vless_reality", "vless_client_uuid": clientID}
	absent := map[string]any{"operation_id": "63000000-0000-4000-8000-000000000091", "credential_id": credentialID, "desired_revision": 2, "state": "absent"}
	client := func(address, publicKey, shortID string) map[string]any {
		return map[string]any{
			"log":      map[string]any{"access": "none", "dnsLog": false, "loglevel": "warning"},
			"inbounds": []any{map[string]any{"listen": "0.0.0.0", "port": 1080, "protocol": "socks", "settings": map[string]any{"udp": false}}},
			"outbounds": []any{map[string]any{
				"protocol": "vless",
				"settings": map[string]any{
					"address":    address,
					"port":       443,
					"id":         clientID,
					"encryption": "none",
					"flow":       "xtls-rprx-vision",
				},
				"streamSettings": map[string]any{"network": "raw", "security": "reality", "realitySettings": map[string]any{"serverName": "camouflage.local", "fingerprint": "chrome", "password": publicKey, "shortId": shortID, "spiderX": "/"}},
			}},
		}
	}
	for name, value := range map[string]any{
		"smoke-present.json":         present,
		"smoke-absent.json":          absent,
		"smoke-client.json":          client("node-agent-primary", publicKeys["primary"], "0123456789abcdef"),
		"smoke-client-failover.json": client("node-agent-failover", publicKeys["failover"], "fedcba9876543210"),
	} {
		contents, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		if err := writeFile(filepath.Join(outDir, name), append(contents, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func camouflageCertificate() ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate camouflage key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate camouflage serial: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "camouflage.local"}, DNSNames: []string{"camouflage.local"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create camouflage certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal camouflage key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func writeFile(path string, contents []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, contents, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("protect %s: %w", path, err)
	}
	return nil
}
