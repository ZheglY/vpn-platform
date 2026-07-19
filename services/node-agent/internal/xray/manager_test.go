package xray

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

func TestMain(m *testing.M) {
	if os.Getenv("NODE_AGENT_XRAY_HELPER") == "1" {
		runHelperProcess()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManagerKeepsLastKnownGoodAfterValidationFailure(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	initial := testSnapshot(1)
	if err := manager.Start(ctx, initial); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	before, err := os.ReadFile(manager.currentPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_AGENT_XRAY_VALIDATE_FAIL", "1")
	if err := manager.Apply(ctx, testSnapshot(2)); err == nil {
		t.Fatal("invalid candidate was accepted")
	}
	after, err := os.ReadFile(manager.currentPath())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) || !manager.Healthy() {
		t.Fatal("validation failure changed or stopped last-known-good runtime")
	}
}

func TestManagerRollsBackOneShotRuntimeFailure(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	if err := manager.Start(ctx, testSnapshot(1)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	before, _ := os.ReadFile(manager.currentPath())
	failMarker := filepath.Join(t.TempDir(), "fail-once")
	if err := os.WriteFile(failMarker, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_AGENT_XRAY_RUNTIME_FAIL_ONCE", failMarker)
	if err := manager.Apply(ctx, testSnapshot(2)); err == nil {
		t.Fatal("failed reload did not report an error")
	}
	after, _ := os.ReadFile(manager.currentPath())
	if !slices.Equal(before, after) || !manager.Healthy() {
		t.Fatal("runtime failure did not restore last-known-good configuration")
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("NODE_AGENT_XRAY_HELPER", "1")
	manager, err := NewManager(ManagerConfig{
		BinaryPath: os.Args[0], ConfigDirectory: t.TempDir(), ValidateTimeout: 2 * time.Second, ReloadTimeout: 100 * time.Millisecond, StartupGrace: time.Second,
		Render: RenderConfig{ListenAddress: "127.0.0.1", ListenPort: 1443, RealityTarget: "camouflage.local:443", ServerNames: []string{"camouflage.local"}, RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortIDs: []string{"0123456789abcdef"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testSnapshot(revision int64) domain.Snapshot {
	return domain.Snapshot{NodeID: "61000000-0000-4000-8000-000000000001", ConfigRevision: revision, Credentials: map[string]domain.Credential{}}
}

func runHelperProcess() {
	args := os.Args[1:]
	if slices.Contains(args, "-test") {
		if os.Getenv("NODE_AGENT_XRAY_VALIDATE_FAIL") == "1" {
			os.Exit(1)
		}
		return
	}
	if marker := os.Getenv("NODE_AGENT_XRAY_RUNTIME_FAIL_ONCE"); marker != "" {
		if _, err := os.Stat(marker); err == nil {
			_ = os.Remove(marker)
			os.Exit(1)
		}
	}
	for {
		time.Sleep(time.Second)
	}
}
