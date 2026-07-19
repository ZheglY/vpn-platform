package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound    = errors.New("provisioning resource not found")
	ErrConflict    = errors.New("provisioning durable state conflict")
	ErrSequenceGap = errors.New("credential command sequence gap")
	ErrCapacity    = errors.New("node capacity unavailable")
)

type EventMeta struct {
	EventID           string
	EventType         string
	AggregateID       string
	AggregateSequence int64
	PartitionKey      string
	CorrelationID     string
	CausationID       *string
	OccurredAt        time.Time
	SourceTopic       string
	SourcePartition   int32
	SourceOffset      int64
	PayloadSHA256     string
}

type Command struct {
	OperationID     string `json:"operation_id"`
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
}

type Operation struct {
	ID                 string
	CredentialID       string
	Kind               string
	DesiredRevision    int
	CommandSequence    int64
	Attempts           int
	MaxAttempts        int
	AllocationRevision int
	CorrelationID      string
	CausationEventID   string
}

type CredentialMaterial struct {
	CredentialID    string `json:"credential_id"`
	SubscriptionID  string `json:"subscription_id"`
	Revision        int    `json:"revision"`
	Protocol        string `json:"protocol"`
	VLESSClientUUID string `json:"vless_client_uuid"`
}

type Placement struct {
	SubscriptionID string    `json:"subscription_id"`
	PeriodID       string    `json:"period_id"`
	Region         string    `json:"region"`
	PrimaryNodes   int       `json:"primary_nodes"`
	FailoverNodes  int       `json:"failover_nodes"`
	ValidUntil     time.Time `json:"valid_until"`
}

type Node struct {
	ID                 string
	Region             string
	ManagementURL      string
	ManagementSPIFFEID string
	CapacityLimit      int
	ReservePercent     int
	AllocatedClients   int
	PublicAddress      string
	PublicPort         int
	ServerName         string
	RealityPublicKey   string
	ShortID            string
	SpiderX            string
	Label              string
}

type Allocation struct {
	ID                    string
	CredentialID          string
	DesiredOperationID    string
	Node                  Node
	Role                  string
	Protocol              string
	DesiredRevision       int
	DesiredState          string
	AllocationRevision    int
	State                 string
	AppliedConfigRevision int64
}

type ReconciliationCandidate struct {
	Allocation Allocation
	ClaimID    string
}

type Endpoint struct {
	NodeID           string `json:"node_id"`
	Role             string `json:"role"`
	Address          string `json:"address"`
	Port             int    `json:"port"`
	ServerName       string `json:"server_name"`
	RealityPublicKey string `json:"reality_public_key"`
	ShortID          string `json:"short_id"`
	SpiderX          string `json:"spider_x,omitempty"`
	Label            string `json:"label"`
}

type AgentDesiredState struct {
	OperationID     string `json:"operation_id"`
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
	State           string `json:"state"`
	Protocol        string `json:"protocol,omitempty"`
	VLESSClientUUID string `json:"vless_client_uuid,omitempty"`
}

type AgentResult struct {
	OperationID     string    `json:"operation_id"`
	CredentialID    string    `json:"credential_id"`
	DesiredRevision int       `json:"desired_revision"`
	State           string    `json:"state"`
	ConfigRevision  int64     `json:"config_revision"`
	AppliedAt       time.Time `json:"applied_at"`
}

type AgentStatus struct {
	NodeID         string `json:"node_id"`
	ConfigRevision int64  `json:"config_revision"`
	ActiveClients  int    `json:"active_clients"`
	XrayHealthy    bool   `json:"xray_healthy"`
	AgentVersion   string `json:"agent_version"`
	XrayVersion    string `json:"xray_version"`
}

type CredentialActualState struct {
	CredentialID    string `json:"credential_id"`
	DesiredRevision int    `json:"desired_revision"`
	State           string `json:"state"`
	ConfigRevision  int64  `json:"config_revision"`
}

type OutboxMessage struct {
	EventID      string
	Topic        string
	PartitionKey string
	Payload      []byte
	Attempts     int
}

type NodeSeed struct {
	ID                 string `json:"id"`
	Region             string `json:"region"`
	ManagementURL      string `json:"management_url"`
	ManagementSPIFFEID string `json:"management_spiffe_id"`
	CapacityLimit      int    `json:"capacity_limit"`
	ReservePercent     int    `json:"reserve_percent"`
	PublicAddress      string `json:"public_address"`
	PublicPort         int    `json:"public_port"`
	ServerName         string `json:"server_name"`
	RealityPublicKey   string `json:"reality_public_key"`
	ShortID            string `json:"short_id"`
	SpiderX            string `json:"spider_x"`
	Label              string `json:"label"`
}

type Store interface {
	Ping(context.Context) error
	RecordCommand(context.Context, EventMeta, Command, string, int) error
	RecordDeadLetter(context.Context, string, int32, int64, string, string) error
	ClaimOperation(context.Context, time.Duration) (Operation, bool, error)
	EnsureAllocations(context.Context, Operation, Placement, string) ([]Allocation, error)
	ListAllocations(context.Context, string) ([]Allocation, error)
	PrepareRevoke(context.Context, Operation) error
	MarkAllocationApplied(context.Context, string, string, int64) error
	MarkAllocationRevoked(context.Context, string, string, int64) error
	MarkAllocationFailed(context.Context, string, string, string) error
	RetryOperation(context.Context, string, string, time.Duration) error
	CompleteProvision(context.Context, Operation, string, []Allocation) error
	CompleteProvisionFailure(context.Context, Operation, string, string, []string) error
	CompleteRevoke(context.Context, Operation, []Allocation) error
	CompleteRevokeFailure(context.Context, Operation, string, []string) error
	ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error)
	CompleteOutbox(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
	SeedNodes(context.Context, []NodeSeed) error
	ListNodes(context.Context) ([]Node, error)
	RecordNodeHealth(context.Context, AgentStatus, time.Duration) error
	MarkNodeOffline(context.Context, string) error
	ClaimReconciliationCandidates(context.Context, int, time.Duration) ([]ReconciliationCandidate, error)
	RescheduleReconciliation(context.Context, string, string, time.Duration) error
}

type AccessClient interface {
	GetCredentialMaterial(context.Context, string) (CredentialMaterial, error)
}

type SubscriptionClient interface {
	GetPlacement(context.Context, string) (Placement, error)
}

type AgentClient interface {
	Apply(context.Context, Node, AgentDesiredState) (AgentResult, error)
	Status(context.Context, Node) (AgentStatus, error)
	CredentialState(context.Context, Node, string) (CredentialActualState, error)
}
