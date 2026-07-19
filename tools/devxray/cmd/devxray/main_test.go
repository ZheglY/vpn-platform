package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunGeneratesDistinctNodeKeysAndSeed(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "dev-xray")
	if err := run(directory); err != nil {
		t.Fatal(err)
	}
	primary, err := os.ReadFile(filepath.Join(directory, "primary.key"))
	if err != nil {
		t.Fatal(err)
	}
	failover, err := os.ReadFile(filepath.Join(directory, "failover.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(primary) == string(failover) || len(primary) != 44 || len(failover) != 44 {
		t.Fatal("REALITY private keys are invalid or reused")
	}
	contents, err := os.ReadFile(filepath.Join(directory, "nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var seeds []nodeSeed
	if err := json.Unmarshal(contents, &seeds); err != nil || len(seeds) != 2 || seeds[0].ID == seeds[1].ID || seeds[0].RealityPublicKey == seeds[1].RealityPublicKey {
		t.Fatalf("node seed is invalid: %+v, %v", seeds, err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"primary.key", "failover.key", "camouflage.key", "nodes.json", "smoke-present.json", "smoke-absent.json", "smoke-client.json"} {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("%s permissions = %v, %v", name, info.Mode().Perm(), err)
			}
		}
	}
}

func TestVPNComposeSmokeUsesFullControlPlane(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "scripts")
	wrapper, err := os.ReadFile(filepath.Join(root, "vpn-smoke.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join(root, "compose-smoke.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(wrapper) + string(harness)
	for _, required := range []string{"provisioning-service", "access-service", "subscription-service", "kafka", "access.provision.request.v1"} {
		if !strings.Contains(script, required) {
			t.Fatalf("vpn-smoke does not exercise %s", required)
		}
	}
	if strings.Contains(script, "Invoke-Desired") {
		t.Fatal("vpn-smoke still bypasses Kafka and provisioning-service by calling node-agent directly")
	}
	if !strings.Contains(string(wrapper), "VPN_SMOKE_FULL_CONTROL_PLANE") {
		t.Fatal("vpn-smoke does not enable the full control-plane harness")
	}
}
