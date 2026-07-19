package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConflict = errors.New("node-agent operation conflict")
	ErrStale    = errors.New("node-agent desired revision is stale")
	ErrNotFound = errors.New("node-agent credential not found")
)

type DesiredState struct {
	OperationID     string `json:"operation_id"`
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
	State           string `json:"state"`
	Protocol        string `json:"protocol,omitempty"`
	VLESSClientUUID string `json:"vless_client_uuid,omitempty"`
}

type ApplyResult struct {
	OperationID     string    `json:"operation_id"`
	CredentialID    string    `json:"credential_id"`
	DesiredRevision int       `json:"desired_revision"`
	State           string    `json:"state"`
	ConfigRevision  int64     `json:"config_revision"`
	AppliedAt       time.Time `json:"applied_at"`
}

type Credential struct {
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
	State           string `json:"state"`
	Protocol        string `json:"protocol,omitempty"`
	VLESSClientUUID string `json:"vless_client_uuid,omitempty"`
}

type Snapshot struct {
	NodeID         string                `json:"node_id"`
	ConfigRevision int64                 `json:"config_revision"`
	Credentials    map[string]Credential `json:"credentials"`
}

type JournalEntry struct {
	OperationID string      `json:"operation_id"`
	RequestHash string      `json:"request_hash"`
	Result      ApplyResult `json:"result"`
	CompletedAt time.Time   `json:"completed_at"`
}

type Journal struct {
	Entries map[string]JournalEntry `json:"entries"`
}

type Status struct {
	NodeID         string `json:"node_id"`
	ConfigRevision int64  `json:"config_revision"`
	ActiveClients  int    `json:"active_clients"`
	XrayHealthy    bool   `json:"xray_healthy"`
	AgentVersion   string `json:"agent_version"`
	XrayVersion    string `json:"xray_version"`
}

type CredentialState struct {
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
	State           string `json:"state"`
	ConfigRevision  int64  `json:"config_revision"`
}

type StateStore interface {
	Load(context.Context, string) (Snapshot, Journal, error)
	SaveSnapshot(context.Context, Snapshot) error
	SaveJournal(context.Context, Journal) error
}

type XrayManager interface {
	Start(context.Context, Snapshot) error
	Apply(context.Context, Snapshot) error
	Healthy() bool
	Version(context.Context) string
	Close(context.Context) error
}
