package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("subscription resource not found")
	ErrConflict = errors.New("subscription event conflicts with durable state")
)

const (
	StatusPending = "pending"
	StatusActive  = "active"
	StatusGrace   = "grace"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

type EventMeta struct {
	EventID       string
	EventType     string
	AggregateID   string
	CorrelationID string
	CausationID   *string
	OccurredAt    time.Time
}

type PaymentSucceeded struct {
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	UserID      string    `json:"user_id"`
	PlanID      string    `json:"plan_id"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	PaidAt      time.Time `json:"paid_at"`
}

type RefundSucceeded struct {
	RefundID    string    `json:"refund_id"`
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	UserID      string    `json:"user_id"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	RefundScope string    `json:"refund_scope"`
	RefundedAt  time.Time `json:"refunded_at"`
}

type PlanSnapshot struct {
	PlanID           string `json:"plan_id"`
	DurationDays     int    `json:"duration_days"`
	GracePeriodHours int    `json:"grace_period_hours"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	Region           string `json:"region"`
}

type Order struct {
	OrderID      string       `json:"order_id"`
	UserID       string       `json:"user_id"`
	Status       string       `json:"status"`
	AmountMinor  int64        `json:"amount_minor"`
	Currency     string       `json:"currency"`
	PlanSnapshot PlanSnapshot `json:"plan_snapshot"`
}

type Subscription struct {
	SubscriptionID     string     `json:"subscription_id"`
	UserID             string     `json:"user_id"`
	Status             string     `json:"status"`
	CurrentPeriodStart *time.Time `json:"current_period_start"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end"`
	GraceEndsAt        *time.Time `json:"grace_ends_at"`
}

type RefundWork struct {
	InboxID  string
	Meta     EventMeta
	Refund   RefundSucceeded
	Attempts int
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
	RecordPaymentReplay(context.Context, EventMeta, PaymentSucceeded, time.Time) (bool, error)
	ApplyPayment(context.Context, EventMeta, PaymentSucceeded, Order, time.Time) error
	StoreRefund(context.Context, EventMeta, RefundSucceeded, time.Time) error
	ClaimRefund(context.Context, time.Duration) (RefundWork, bool, error)
	ApplyClaimedRefund(context.Context, RefundWork, time.Time, time.Duration) error
	ClaimDue(context.Context, time.Time, time.Duration) (Subscription, bool, error)
	CompleteDue(context.Context, string, time.Time) error
	GetSubscription(context.Context, string) (Subscription, error)
	RecordDeadLetter(context.Context, string, int32, int64, string, string) error
	ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error)
	CompleteOutbox(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
}

type Billing interface {
	GetOrder(context.Context, string, string) (Order, error)
	Ping(context.Context) error
}
