package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound             = errors.New("access resource not found")
	ErrNotReady             = errors.New("access is not ready")
	ErrAlreadyIssued        = errors.New("subscription URL already issued")
	ErrIdempotencyReplay    = errors.New("idempotency response is unavailable")
	ErrIdempotencyConflict  = errors.New("idempotency key conflicts with request")
	ErrDurableStateConflict = errors.New("event conflicts with durable access state")
	ErrLifecycleSequenceGap = errors.New("subscription lifecycle sequence gap")
	ErrOutcomeSequenceGap   = errors.New("provisioning outcome sequence gap")
	ErrRecoveryNotAllowed   = errors.New("provisioning recovery is not allowed")
)

const (
	StatusProvisioning = "provisioning"
	StatusActive       = "active"
	StatusDegraded     = "degraded"
	StatusFailed       = "failed"
	StatusRevoking     = "revoking"
	StatusRevoked      = "revoked"
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

type PeriodEvent struct {
	SubscriptionID  string    `json:"subscription_id"`
	UserID          string    `json:"user_id"`
	PeriodID        string    `json:"period_id"`
	SourceOrderID   string    `json:"source_order_id"`
	SourcePaymentID string    `json:"source_payment_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	GraceEndsAt     time.Time `json:"grace_ends_at"`
}

type TerminalEvent struct {
	SubscriptionID    string    `json:"subscription_id"`
	UserID            string    `json:"user_id"`
	Reason            string    `json:"reason"`
	AffectedPeriodIDs []string  `json:"affected_period_ids,omitempty"`
	EffectiveAt       time.Time `json:"effective_at"`
}

type GraceEvent struct {
	SubscriptionID string    `json:"subscription_id"`
	UserID         string    `json:"user_id"`
	PeriodEnd      time.Time `json:"period_end"`
	GraceEndsAt    time.Time `json:"grace_ends_at"`
	EffectiveAt    time.Time `json:"effective_at"`
}

type EndpointSnapshot struct {
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

type ProvisionSucceeded struct {
	OperationID     string             `json:"operation_id"`
	CredentialID    string             `json:"credential_id"`
	AppliedRevision int                `json:"applied_revision"`
	Status          string             `json:"status"`
	AssignedNodeIDs []string           `json:"assigned_node_ids"`
	Endpoints       []EndpointSnapshot `json:"endpoints"`
	AppliedAt       time.Time          `json:"applied_at"`
}

type OperationFailed struct {
	OperationID    string    `json:"operation_id"`
	CredentialID   string    `json:"credential_id"`
	FailedRevision int       `json:"failed_revision"`
	FailureScope   string    `json:"failure_scope"`
	Terminal       bool      `json:"terminal"`
	ReasonCode     string    `json:"reason_code"`
	PendingNodeIDs []string  `json:"pending_node_ids,omitempty"`
	FailedAt       time.Time `json:"failed_at"`
}

type RevokeSucceeded struct {
	OperationID             string    `json:"operation_id"`
	CredentialID            string    `json:"credential_id"`
	DesiredRevision         int       `json:"desired_revision"`
	AllocationRevision      int       `json:"allocation_revision"`
	AllAssignedNodesRemoved bool      `json:"all_assigned_nodes_removed"`
	NodeIDs                 []string  `json:"node_ids"`
	RevokedAt               time.Time `json:"revoked_at"`
}

type CredentialSeed struct {
	CredentialID string
	OperationID  string
	Ciphertext   []byte
	KeyVersion   int
}

type TokenSeed struct {
	TokenID        string
	LookupHMAC     []byte
	IdempotencyKey string
	RequestSHA256  string
}

type AccessStatus struct {
	SubscriptionID     string `json:"subscription_id"`
	CredentialID       string `json:"credential_id"`
	AccessStatus       string `json:"access_status"`
	ProvisioningStatus string `json:"provisioning_status"`
	TokenStatus        string `json:"token_status"`
}

type AdminRecoveryInput struct {
	CredentialID   string
	OperationID    string
	IdempotencyKey string
	RequestSHA256  string
	ActionID       string
	CorrelationID  string
}

type AdminRecoveryResult struct {
	CredentialID    string `json:"credential_id"`
	OperationID     string `json:"operation_id"`
	DesiredRevision int    `json:"desired_revision"`
	Status          string `json:"status"`
	Replay          bool   `json:"replay"`
}

type ProfileRecord struct {
	CredentialID         string
	Ciphertext           []byte
	KeyVersion           int
	EntitlementExpiresAt time.Time
	Endpoints            []EndpointSnapshot
}

type ProvisioningRecord struct {
	CredentialID   string
	SubscriptionID string
	Revision       int
	Ciphertext     []byte
	KeyVersion     int
}

type OutboxMessage struct {
	EventID      string
	Topic        string
	PartitionKey string
	Payload      []byte
	Attempts     int
}

type Store interface {
	Ping(context.Context) error
	ApplyPeriod(context.Context, EventMeta, PeriodEvent, CredentialSeed) error
	ApplyGrace(context.Context, EventMeta, GraceEvent) error
	ApplyTerminal(context.Context, EventMeta, TerminalEvent, string) error
	ApplyProvisionSucceeded(context.Context, EventMeta, ProvisionSucceeded) error
	ApplyOperationFailed(context.Context, EventMeta, OperationFailed, string) error
	ApplyRevokeSucceeded(context.Context, EventMeta, RevokeSucceeded) error
	IssueToken(context.Context, string, string, TokenSeed) error
	GetAccessStatus(context.Context, string) (AccessStatus, error)
	RecoverProvisioning(context.Context, AdminRecoveryInput) (AdminRecoveryResult, error)
	GetProfileByTokenHMAC(context.Context, []byte) (ProfileRecord, error)
	GetProvisioningRecord(context.Context, string) (ProvisioningRecord, error)
	RecordCredentialMaterialAccess(context.Context, string, string) error
	RecordDeadLetter(context.Context, string, int32, int64, string, string) error
	ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error)
	CompleteOutbox(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
}
