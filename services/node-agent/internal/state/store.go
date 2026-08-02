package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

type Store struct {
	dir        string
	maxJournal int
}

func NewStore(dir string, maxJournal int) (*Store, error) {
	if dir == "" || maxJournal < 1 {
		return nil, fmt.Errorf("node state directory and journal limit are required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create node state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("protect node state directory: %w", err)
	}
	return &Store{dir: dir, maxJournal: maxJournal}, nil
}

func (s *Store) Load(_ context.Context, nodeID string) (domain.Snapshot, domain.Journal, error) {
	snapshot := domain.Snapshot{NodeID: nodeID, Credentials: make(map[string]domain.Credential)}
	journal := domain.Journal{Entries: make(map[string]domain.JournalEntry)}
	if err := readJSON(filepath.Join(s.dir, "desired-state.json"), &snapshot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.Snapshot{}, domain.Journal{}, fmt.Errorf("load node desired state: %w", err)
	}
	if snapshot.NodeID != nodeID || snapshot.ConfigRevision < 0 {
		return domain.Snapshot{}, domain.Journal{}, fmt.Errorf("node desired state identity is invalid")
	}
	if snapshot.Credentials == nil {
		snapshot.Credentials = make(map[string]domain.Credential)
	}
	if err := readJSON(filepath.Join(s.dir, "operation-journal.json"), &journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.Snapshot{}, domain.Journal{}, fmt.Errorf("load node operation journal: %w", err)
	}
	if journal.Entries == nil {
		journal.Entries = make(map[string]domain.JournalEntry)
	}
	return snapshot, journal, nil
}

func (s *Store) SaveSnapshot(_ context.Context, snapshot domain.Snapshot) error {
	return writeJSONAtomic(filepath.Join(s.dir, "desired-state.json"), snapshot)
}

func (s *Store) SaveJournal(_ context.Context, journal domain.Journal) error {
	if len(journal.Entries) > s.maxJournal {
		for len(journal.Entries) > s.maxJournal {
			var oldestKey string
			for key, entry := range journal.Entries {
				if oldestKey == "" || entry.CompletedAt.Before(journal.Entries[oldestKey].CompletedAt) {
					oldestKey = key
				}
			}
			delete(journal.Entries, oldestKey)
		}
	}
	return writeJSONAtomic(filepath.Join(s.dir, "operation-journal.json"), journal)
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".node-state-*")
	if err != nil {
		return fmt.Errorf("create node state candidate: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect node state candidate: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode node state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync node state candidate: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close node state candidate: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace node state: %w", err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}
