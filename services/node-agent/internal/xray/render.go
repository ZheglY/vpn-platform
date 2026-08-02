package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

type RenderConfig struct {
	ListenAddress     string
	ListenPort        int
	RealityTarget     string
	ServerNames       []string
	RealityPrivateKey string
	ShortIDs          []string
}

func Render(snapshot domain.Snapshot, cfg RenderConfig) ([]byte, error) {
	if cfg.ListenAddress == "" || cfg.ListenPort < 1 || cfg.ListenPort > 65535 || cfg.RealityTarget == "" || len(cfg.ServerNames) == 0 || len(cfg.RealityPrivateKey) != 43 || len(cfg.ShortIDs) == 0 {
		return nil, fmt.Errorf("xray render configuration is invalid")
	}
	credentialIDs := make([]string, 0, len(snapshot.Credentials))
	for credentialID, credential := range snapshot.Credentials {
		if credential.State == "present" {
			credentialIDs = append(credentialIDs, credentialID)
		}
	}
	sort.Strings(credentialIDs)
	clients := make([]map[string]any, 0, len(credentialIDs))
	for _, credentialID := range credentialIDs {
		credential := snapshot.Credentials[credentialID]
		if credential.Protocol != "vless_reality" || credential.VLESSClientUUID == "" {
			return nil, fmt.Errorf("xray credential state is invalid")
		}
		clients = append(clients, map[string]any{"id": credential.VLESSClientUUID, "level": 0, "flow": "xtls-rprx-vision"})
	}
	config := map[string]any{
		"log": map[string]any{"access": "none", "dnsLog": false, "loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": cfg.ListenAddress, "port": cfg.ListenPort, "protocol": "vless", "tag": "vless-reality",
			"settings": map[string]any{"clients": clients, "decryption": "none"},
			"streamSettings": map[string]any{
				"network": "raw", "security": "reality",
				"realitySettings": map[string]any{"show": false, "target": cfg.RealityTarget, "xver": 0, "serverNames": cfg.ServerNames, "privateKey": cfg.RealityPrivateKey, "shortIds": cfg.ShortIDs},
			},
		}},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "tag": "direct"},
			map[string]any{"protocol": "blackhole", "tag": "blocked"},
		},
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Xray candidate: %w", err)
	}
	return append(contents, '\n'), nil
}

func LoadPrivateKey(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read REALITY private key: %w", err)
	}
	key := strings.TrimSpace(string(contents))
	if len(key) != 43 {
		return "", fmt.Errorf("REALITY private key is invalid")
	}
	for _, character := range key {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return "", fmt.Errorf("REALITY private key is invalid")
		}
	}
	return key, nil
}
