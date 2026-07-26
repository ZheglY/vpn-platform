package xray

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

type SystemdManagerConfig struct {
	BinaryPath      string
	ConfigDirectory string
	ControlCommand  string
	ControlArgs     []string
	Render          RenderConfig
	ValidateTimeout time.Duration
	ReloadTimeout   time.Duration
	StartupGrace    time.Duration
}

type SystemdManager struct {
	mu      sync.Mutex
	config  SystemdManagerConfig
	metrics Observer
}

func NewSystemdManager(config SystemdManagerConfig, observers ...Observer) (*SystemdManager, error) {
	if config.BinaryPath == "" || config.ConfigDirectory == "" || config.ControlCommand == "" ||
		config.ValidateTimeout <= 0 || config.ReloadTimeout <= 0 || config.StartupGrace <= 0 {
		return nil, fmt.Errorf("systemd Xray manager configuration is invalid")
	}
	info, err := os.Stat(config.ConfigDirectory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o007 != 0 {
		return nil, fmt.Errorf("systemd Xray configuration directory is unavailable or exposed")
	}
	observer := Observer(noopObserver{})
	if len(observers) > 0 && observers[0] != nil {
		observer = observers[0]
	}
	return &SystemdManager{config: config, metrics: observer}, nil
}

func (m *SystemdManager) Start(ctx context.Context, snapshot domain.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.install(ctx, snapshot, false)
	return err
}

func (m *SystemdManager) Apply(ctx context.Context, snapshot domain.Snapshot) error {
	startedAt := time.Now()
	m.mu.Lock()
	outcome, err := m.install(ctx, snapshot, true)
	m.mu.Unlock()
	m.metrics.ObserveReload(outcome, time.Since(startedAt))
	return err
}

func (m *SystemdManager) install(ctx context.Context, snapshot domain.Snapshot, keepPrevious bool) (string, error) {
	candidate, err := m.writeCandidate(snapshot)
	if err != nil {
		return "error", err
	}
	defer func() { _ = os.Remove(candidate) }()
	if err := m.validate(ctx, candidate); err != nil {
		return "validation_error", err
	}
	current, backup := m.currentPath(), m.backupPath()
	hadCurrent := false
	if _, err := os.Stat(current); err == nil {
		hadCurrent = true
		_ = os.Remove(backup)
		if err := os.Rename(current, backup); err != nil {
			return "error", fmt.Errorf("preserve last-known-good Xray configuration: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "error", fmt.Errorf("inspect current Xray configuration: %w", err)
	}
	if err := os.Rename(candidate, current); err != nil {
		if hadCurrent {
			if restoreErr := os.Rename(backup, current); restoreErr != nil {
				return "rollback_failed", fmt.Errorf("install Xray candidate and restore last-known-good")
			}
			return "rollback_restored", fmt.Errorf("install Xray candidate: %w", err)
		}
		return "error", fmt.Errorf("install initial Xray candidate: %w", err)
	}
	if err := m.control(ctx, "reload"); err == nil && m.waitHealthy(ctx) {
		if !keepPrevious {
			_ = os.Remove(backup)
		}
		return "success", nil
	}
	_ = os.Remove(current)
	if !hadCurrent {
		return "rollback_failed", fmt.Errorf("start Xray systemd unit with initial configuration")
	}
	if err := os.Rename(backup, current); err != nil {
		return "rollback_failed", fmt.Errorf("restore last-known-good Xray configuration")
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.config.ReloadTimeout+m.config.StartupGrace)
	defer cancel()
	if err := m.control(rollbackCtx, "reload"); err != nil || !m.waitHealthy(rollbackCtx) {
		return "rollback_failed", fmt.Errorf("xray systemd reload and rollback failed")
	}
	return "rollback_restored", fmt.Errorf("xray systemd reload failed; last-known-good restored")
}

func (m *SystemdManager) Healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return m.control(ctx, "status") == nil
}

func (m *SystemdManager) Version(ctx context.Context) string {
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, m.config.BinaryPath, "version").Output()
	if err != nil {
		return "unknown"
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if len(line) > 64 {
		line = line[:64]
	}
	return line
}

func (m *SystemdManager) Close(context.Context) error {
	return nil
}

func (m *SystemdManager) writeCandidate(snapshot domain.Snapshot) (string, error) {
	contents, err := Render(snapshot, m.config.Render)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(m.config.ConfigDirectory, ".candidate-*.json")
	if err != nil {
		return "", fmt.Errorf("create Xray candidate: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o640); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("protect Xray candidate: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write Xray candidate: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync Xray candidate: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close Xray candidate: %w", err)
	}
	return path, nil
}

func (m *SystemdManager) validate(ctx context.Context, path string) error {
	validateCtx, cancel := context.WithTimeout(ctx, m.config.ValidateTimeout)
	defer cancel()
	command := exec.CommandContext(validateCtx, m.config.BinaryPath, "run", "-test", "-config", path)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("xray candidate validation failed")
	}
	return nil
}

func (m *SystemdManager) control(ctx context.Context, action string) error {
	if action != "reload" && action != "status" {
		return fmt.Errorf("unsupported Xray control action")
	}
	controlCtx, cancel := context.WithTimeout(ctx, m.config.ReloadTimeout)
	defer cancel()
	args := append(append([]string(nil), m.config.ControlArgs...), action)
	command := exec.CommandContext(controlCtx, m.config.ControlCommand, args...)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("xray control action failed")
	}
	return nil
}

func (m *SystemdManager) waitHealthy(ctx context.Context) bool {
	deadline := time.NewTimer(m.config.StartupGrace)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return m.control(ctx, "status") == nil
		case <-ticker.C:
			if m.control(ctx, "status") == nil {
				return true
			}
		}
	}
}

func (m *SystemdManager) currentPath() string {
	return filepath.Join(m.config.ConfigDirectory, "config.json")
}

func (m *SystemdManager) backupPath() string {
	return filepath.Join(m.config.ConfigDirectory, "last-known-good.json")
}
