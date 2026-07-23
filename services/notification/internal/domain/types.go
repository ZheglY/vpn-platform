package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("notification not found")
	ErrEventConflict       = errors.New("notification event conflicts with durable state")
	ErrSequenceGap         = errors.New("notification aggregate sequence gap")
	ErrIdempotencyConflict = errors.New("notification idempotency conflict")
	ErrRetryNotAllowed     = errors.New("notification retry is not allowed")
)

const (
	StatusPending           = "pending"
	StatusProcessing        = "processing"
	StatusRetry             = "retry"
	StatusDelivered         = "delivered"
	StatusPermanentlyFailed = "permanently_failed"
	StatusSuppressed        = "suppressed"
)

type EventMeta struct {
	EventID           string
	EventType         string
	Producer          string
	AggregateType     string
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

type Intent struct {
	NotificationID    string
	UserID            string
	SubscriptionID    *string
	CredentialID      *string
	NotificationType  string
	TemplateVersion   int
	BusinessDedupeKey string
	Variables         map[string]string
	SuppressedReason  string
	MaxAttempts       int
}

type Job struct {
	NotificationID   string            `json:"notification_id"`
	UserID           string            `json:"user_id"`
	SubscriptionID   *string           `json:"subscription_id,omitempty"`
	CredentialID     *string           `json:"credential_id,omitempty"`
	NotificationType string            `json:"notification_type"`
	TemplateVersion  int               `json:"template_version"`
	Status           string            `json:"status"`
	Attempts         int               `json:"attempts"`
	MaxAttempts      int               `json:"max_attempts"`
	NextAttemptAt    time.Time         `json:"next_attempt_at"`
	ClaimID          string            `json:"-"`
	CorrelationID    string            `json:"correlation_id"`
	CausationID      *string           `json:"causation_id,omitempty"`
	Variables        map[string]string `json:"-"`
	TerminalReason   *string           `json:"terminal_reason_code,omitempty"`
	DeliveredAt      *time.Time        `json:"delivered_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type TelegramTarget struct {
	Eligible       bool
	ReasonCode     string
	TelegramChatID int64
	Locale         string
}

type DeliveryResult struct {
	Replay bool
}

type DeliveryError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

type DeadLetter struct {
	SourceTopic     string    `json:"source_topic"`
	SourcePartition int32     `json:"source_partition"`
	SourceOffset    int64     `json:"source_offset"`
	PayloadSHA256   string    `json:"payload_sha256"`
	EventType       *string   `json:"event_type,omitempty"`
	ReasonCode      string    `json:"reason_code"`
	State           string    `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (e *DeliveryError) Error() string { return "telegram delivery failed: " + e.Code }

type Store interface {
	Ping(context.Context) error
	RecordEvent(context.Context, EventMeta, Intent) (bool, error)
	RecordDeadLetter(context.Context, string, int32, int64, string, string, string) error
	ClaimJob(context.Context, time.Duration) (Job, bool, error)
	CompleteJob(context.Context, string, string) error
	RetryJob(context.Context, string, string, time.Duration) error
	FailJob(context.Context, string, string, string) error
	SuppressJob(context.Context, string, string, string) error
	GetJob(context.Context, string) (Job, error)
	RequestRetry(context.Context, string, string, string) (Job, bool, error)
	ListDeadLetters(context.Context, int) ([]DeadLetter, error)
	Close()
}

type IdentityClient interface {
	GetNotificationTarget(context.Context, string) (TelegramTarget, error)
}

type AccessClient interface {
	IsCurrentReady(context.Context, string, string) (bool, error)
}

type SubscriptionClient interface {
	IsEntitled(context.Context, string, string) (bool, error)
}

type TelegramClient interface {
	Deliver(context.Context, string, int64, string, int, string) (DeliveryResult, error)
}
