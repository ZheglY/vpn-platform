package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
