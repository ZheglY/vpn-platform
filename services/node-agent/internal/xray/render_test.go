package xray

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

func TestRenderIsDeterministicAndDisablesAccessLog(t *testing.T) {
	snapshot := domain.Snapshot{NodeID: "61000000-0000-4000-8000-000000000001", ConfigRevision: 3, Credentials: map[string]domain.Credential{
		"62000000-0000-4000-8000-000000000002": {CredentialID: "62000000-0000-4000-8000-000000000002", DesiredRevision: 1, State: "present", Protocol: "vless_reality", VLESSClientUUID: "64000000-0000-4000-8000-000000000002"},
		"62000000-0000-4000-8000-000000000001": {CredentialID: "62000000-0000-4000-8000-000000000001", DesiredRevision: 2, State: "present", Protocol: "vless_reality", VLESSClientUUID: "64000000-0000-4000-8000-000000000001"},
	}}
	cfg := RenderConfig{ListenAddress: "127.0.0.1", ListenPort: 1443, RealityTarget: "camouflage.local:443", ServerNames: []string{"camouflage.local"}, RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortIDs: []string{"0123456789abcdef"}}
	first, err := Render(snapshot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(snapshot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("rendered configuration is not deterministic")
	}
	for _, required := range [][]byte{[]byte(`"access": "none"`), []byte(`"flow": "xtls-rprx-vision"`), []byte(`"security": "reality"`)} {
		if !bytes.Contains(first, required) {
			t.Fatalf("rendered configuration does not contain %s", required)
		}
	}
	if bytes.Index(first, []byte("64000000-0000-4000-8000-000000000001")) > bytes.Index(first, []byte("64000000-0000-4000-8000-000000000002")) {
		t.Fatal("rendered clients are not stable by credential id")
	}
	var decoded struct {
		Inbounds []struct {
			Settings map[string]json.RawMessage `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Inbounds) != 1 || decoded.Inbounds[0].Settings["clients"] == nil || decoded.Inbounds[0].Settings["users"] != nil {
		t.Fatal("rendered config does not use the clients field required by pinned Xray 26.3.27")
	}
}
