package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrUnauthenticated     = errors.New("administrator identity is not authenticated")
	ErrForbidden           = errors.New("administrator is not authorized")
	ErrNotFound            = errors.New("admin resource not found")
	ErrIdempotencyConflict = errors.New("admin idempotency conflict")
	ErrActionStateConflict = errors.New("admin action state conflict")
	ErrActionInProgress    = errors.New("admin action is already in progress")
)

const (
	ActionNotificationRetry  = "notification.retry"
	ActionSubscriptionRevoke = "subscription.revoke"
	ActionAccessRecover      = "access.provisioning.recover"
)

type Principal struct {
	SPIFFEID    string   `json:"spiffe_id"`
	Name        string   `json:"principal"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

type ActionInput struct {
	ActionID             string
	Actor                Principal
	Action               string
	Permission           string
	TargetType           string
	TargetID             string
	Reason               string
	ReasonCode           string
	IdempotencyKeySHA256 string
	RequestSHA256        string
	RequestID            string
	CorrelationID        string
}

type Action struct {
	ActionID      string          `json:"action_id"`
	Action        string          `json:"action"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result,omitempty"`
	ErrorCode     *string         `json:"error_code,omitempty"`
	CorrelationID string          `json:"correlation_id"`
	CreatedAt     time.Time       `json:"created_at"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	Replay        bool            `json:"replay"`
	Attempts      int             `json:"-"`
	ClaimID       string          `json:"-"`
	LeaseUntil    *time.Time      `json:"-"`
}

type AuditEvent struct {
	AuditEventID           string     `json:"audit_event_id"`
	ActionID               string     `json:"action_id"`
	ActorPrincipal         string     `json:"actor_principal"`
	VerifiedSPIFFEIdentity string     `json:"verified_spiffe_identity"`
	Roles                  []string   `json:"roles"`
	Permissions            []string   `json:"permissions"`
	Action                 string     `json:"action"`
	Permission             string     `json:"permission"`
	TargetType             string     `json:"target_type"`
	TargetID               string     `json:"target_id"`
	Reason                 string     `json:"reason"`
	Outcome                string     `json:"outcome"`
	ErrorCode              *string    `json:"error_code,omitempty"`
	RequestID              string     `json:"request_id"`
	CorrelationID          string     `json:"correlation_id"`
	OccurredAt             time.Time  `json:"occurred_at"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
}

type Store interface {
	Ping(context.Context) error
	GetPrincipal(context.Context, string, string) (Principal, error)
	BeginAction(context.Context, ActionInput) (Action, bool, error)
	StartActionAttempt(context.Context, string, string, time.Duration) (Action, bool, error)
	CompleteAction(context.Context, string, string, string, json.RawMessage, string) (Action, error)
	MarkActionOutcomeUnknown(context.Context, string, string, string, string) (Action, error)
	ListAudit(context.Context, int) ([]AuditEvent, error)
	Close()
}

type OwnerError struct {
	Code       string
	Definitive bool
}

func (e *OwnerError) Error() string { return e.Code }
