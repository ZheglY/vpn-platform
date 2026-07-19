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
	"syscall"
	"time"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

type ManagerConfig struct {
	BinaryPath      string
	ConfigDirectory string
	Render          RenderConfig
	ValidateTimeout time.Duration
	ReloadTimeout   time.Duration
	StartupGrace    time.Duration
}

type Manager struct {
	mu      sync.Mutex
	config  ManagerConfig
	command *exec.Cmd
	done    chan struct{}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.BinaryPath == "" || config.ConfigDirectory == "" || config.ValidateTimeout <= 0 || config.ReloadTimeout <= 0 || config.StartupGrace <= 0 {
		return nil, fmt.Errorf("xray manager configuration is invalid")
	}
	if err := os.MkdirAll(config.ConfigDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create Xray configuration directory: %w", err)
	}
	if err := os.Chmod(config.ConfigDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("protect Xray configuration directory: %w", err)
	}
	return &Manager{config: config}, nil
}

func (m *Manager) Start(ctx context.Context, snapshot domain.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, err := m.writeCandidate(snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(candidate) }()
	if err := m.validate(ctx, candidate); err != nil {
		return err
	}
	current := m.currentPath()
	if err := os.Rename(candidate, current); err != nil {
		return fmt.Errorf("install initial Xray configuration: %w", err)
	}
	if err := m.startLocked(); err != nil {
		return err
	}
	if !m.waitHealthyLocked(m.config.StartupGrace) {
		_ = m.stopLocked(context.Background())
		return fmt.Errorf("xray failed during startup")
	}
	return nil
}

func (m *Manager) Apply(ctx context.Context, snapshot domain.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, err := m.writeCandidate(snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(candidate) }()
	if err := m.validate(ctx, candidate); err != nil {
		return err
	}
	consistencyBound := 2*m.config.ReloadTimeout + 2*m.config.StartupGrace
	consistencyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), consistencyBound)
	defer cancel()
	if err := m.stopLocked(consistencyCtx); err != nil {
		if m.restoreCurrentLocked() {
			return fmt.Errorf("stop Xray before reload; last-known-good restored")
		}
		return fmt.Errorf("stop Xray before reload and restore last-known-good")
	}
	current, backup := m.currentPath(), m.backupPath()
	_ = os.Remove(backup)
	if err := os.Rename(current, backup); err != nil {
		_ = m.restoreCurrentLocked()
		return fmt.Errorf("preserve last-known-good Xray configuration: %w", err)
	}
	if err := os.Rename(candidate, current); err != nil {
		_ = os.Rename(backup, current)
		_ = m.restoreCurrentLocked()
		return fmt.Errorf("install Xray candidate: %w", err)
	}
	if err := m.startLocked(); err == nil && m.waitHealthyLocked(m.config.StartupGrace) {
		_ = os.Remove(backup)
		return nil
	}
	_ = m.stopLocked(consistencyCtx)
	_ = os.Remove(current)
	if err := os.Rename(backup, current); err != nil {
		return fmt.Errorf("xray reload and rollback failed")
	}
	if !m.restoreCurrentLocked() {
		return fmt.Errorf("xray reload and rollback failed")
	}
	return fmt.Errorf("xray reload failed; last-known-good restored")
}

func (m *Manager) restoreCurrentLocked() bool {
	if err := m.startLocked(); err != nil {
		return false
	}
	return m.waitHealthyLocked(m.config.StartupGrace)
}

func (m *Manager) Healthy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command == nil || m.done == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

func (m *Manager) Version(ctx context.Context) string {
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

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *Manager) writeCandidate(snapshot domain.Snapshot) (string, error) {
	contents, err := Render(snapshot, m.config.Render)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(m.config.ConfigDirectory, ".candidate-*.json")
	if err != nil {
		return "", fmt.Errorf("create Xray candidate: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
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

func (m *Manager) validate(ctx context.Context, path string) error {
	validateCtx, cancel := context.WithTimeout(ctx, m.config.ValidateTimeout)
	defer cancel()
	command := exec.CommandContext(validateCtx, m.config.BinaryPath, "run", "-test", "-config", path)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("xray candidate validation failed")
	}
	return nil
}

func (m *Manager) startLocked() error {
	command := exec.Command(m.config.BinaryPath, "run", "-config", m.currentPath())
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Xray process: %w", err)
	}
	done := make(chan struct{})
	m.command, m.done = command, done
	go func() {
		_ = command.Wait()
		close(done)
	}()
	return nil
}

func (m *Manager) stopLocked(ctx context.Context) error {
	if m.command == nil || m.command.Process == nil || m.done == nil {
		return nil
	}
	select {
	case <-m.done:
		m.command, m.done = nil, nil
		return nil
	default:
	}
	_ = m.command.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(m.config.ReloadTimeout)
	defer timer.Stop()
	select {
	case <-m.done:
		m.command, m.done = nil, nil
		return nil
	case <-ctx.Done():
		_ = m.command.Process.Kill()
		<-m.done
		m.command, m.done = nil, nil
		return ctx.Err()
	case <-timer.C:
		_ = m.command.Process.Kill()
		<-m.done
		m.command, m.done = nil, nil
		return nil
	}
}

func (m *Manager) waitHealthyLocked(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-m.done:
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) currentPath() string { return filepath.Join(m.config.ConfigDirectory, "config.json") }
func (m *Manager) backupPath() string {
	return filepath.Join(m.config.ConfigDirectory, "last-known-good.json")
}
