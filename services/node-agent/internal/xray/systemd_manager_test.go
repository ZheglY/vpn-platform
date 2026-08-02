package xray

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

func TestSystemdManagerRestoresLastKnownGoodAfterReloadFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable shell fixtures are validated on Linux")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "xray")
	control := filepath.Join(directory, "control")
	state := filepath.Join(directory, "control-state")
	writeExecutable(t, binary, "#!/bin/sh\nif [ \"$1\" = version ]; then echo test-xray; fi\nexit 0\n")
	writeExecutable(t, control, "#!/bin/sh\nif [ \"$1\" = status ]; then exit 0; fi\ncount=0\n[ ! -f \""+state+"\" ] || count=$(cat \""+state+"\")\ncount=$((count+1)); echo \"$count\" > \""+state+"\"\n[ \"$count\" -ne 2 ]\n")
	observer := &recordingReloadObserver{}
	manager, err := NewSystemdManager(SystemdManagerConfig{
		BinaryPath: binary, ConfigDirectory: directory, ControlCommand: control,
		ValidateTimeout: time.Second, ReloadTimeout: time.Second, StartupGrace: 100 * time.Millisecond,
		Render: RenderConfig{
			ListenAddress: "127.0.0.1", ListenPort: 443, RealityTarget: "example.invalid:443",
			ServerNames: []string{"example.invalid"}, RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			ShortIDs: []string{"0011"},
		},
	}, observer)
	if err != nil {
		t.Fatal(err)
	}
	initial := domain.Snapshot{NodeID: "61000000-0000-4000-8000-000000000001", Credentials: map[string]domain.Credential{}}
	if err := manager.Start(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	next := initial
	next.ConfigRevision = 1
	next.Credentials = map[string]domain.Credential{
		"62000000-0000-4000-8000-000000000001": {
			CredentialID:    "62000000-0000-4000-8000-000000000001",
			DesiredRevision: 1, State: "present", Protocol: "vless_reality",
			VLESSClientUUID: "64000000-0000-4000-8000-000000000001",
		},
	}
	if err := manager.Apply(context.Background(), next); err == nil {
		t.Fatal("failed reload unexpectedly succeeded")
	}
	after, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed reload did not restore last-known-good configuration")
	}
	if observer.outcome != "rollback_restored" {
		t.Fatalf("reload outcome = %q, want rollback_restored", observer.outcome)
	}
}

func TestSystemdManagerStartValidationFailurePreservesCurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable shell fixtures are validated on Linux")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	reject := filepath.Join(directory, "reject")
	binary := filepath.Join(directory, "xray")
	control := filepath.Join(directory, "control")
	writeExecutable(t, binary, "#!/bin/sh\n[ ! -e \""+reject+"\" ]\n")
	writeExecutable(t, control, "#!/bin/sh\nexit 0\n")
	manager, err := NewSystemdManager(SystemdManagerConfig{
		BinaryPath: binary, ConfigDirectory: directory, ControlCommand: control,
		ValidateTimeout: time.Second, ReloadTimeout: time.Second, StartupGrace: 100 * time.Millisecond,
		Render: RenderConfig{
			ListenAddress: "127.0.0.1", ListenPort: 443, RealityTarget: "example.invalid:443",
			ServerNames: []string{"example.invalid"}, RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			ShortIDs: []string{"0011"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.Snapshot{NodeID: "61000000-0000-4000-8000-000000000001", Credentials: map[string]domain.Credential{}}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reject, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), snapshot); err == nil {
		t.Fatal("invalid startup candidate unexpectedly succeeded")
	}
	after, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid startup candidate changed current configuration")
	}
}

type recordingReloadObserver struct {
	outcome string
}

func (o *recordingReloadObserver) ObserveReload(outcome string, _ time.Duration) {
	o.outcome = outcome
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
