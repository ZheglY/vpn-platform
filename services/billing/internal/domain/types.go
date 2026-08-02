package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("billing resource not found")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with prior request")
	ErrStateConflict       = errors.New("billing state conflict")
	ErrUserUnavailable     = errors.New("user is unavailable")
	ErrConsentRequired     = errors.New("consent is required")
)

const (
	OrderStatusCreated        = "created"
	OrderStatusPaymentPending = "payment_pending"
	OrderStatusPaid           = "paid"
	OrderStatusCanceled       = "canceled"

	PaymentStatusCreated             = "created"
	PaymentStatusVerificationPending = "verification_pending"
	PaymentStatusPending             = "pending"
	PaymentStatusSucceeded           = "succeeded"
	PaymentStatusCanceled            = "canceled"
	PaymentStatusFailed              = "failed"
)

type PlanSnapshot struct {
	PlanID           string `json:"plan_id"`
	Name             string `json:"name"`
	DurationDays     int    `json:"duration_days"`
	GracePeriodHours int    `json:"grace_period_hours"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	Region           string `json:"region"`
	RegionPolicy     string `json:"region_policy"`
	TrafficPolicy    string `json:"traffic_policy"`
	PrimaryNodes     int    `json:"primary_nodes"`
	FailoverNodes    int    `json:"failover_nodes"`
}

type Order struct {
	OrderID              string       `json:"order_id"`
	UserID               string       `json:"user_id"`
	Status               string       `json:"status"`
	AmountMinor          int64        `json:"amount_minor"`
	Currency             string       `json:"currency"`
	AcceptedTermsVersion string       `json:"accepted_terms_version"`
	PlanSnapshot         PlanSnapshot `json:"plan_snapshot"`
	CreatedAt            time.Time    `json:"created_at"`
}

type Payment struct {
	PaymentID       string     `json:"payment_id"`
	OrderID         string     `json:"order_id"`
	Status          string     `json:"status"`
	AmountMinor     int64      `json:"amount_minor"`
	Currency        string     `json:"currency"`
	ConfirmationURL *string    `json:"confirmation_url,omitempty"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type PaymentStatus struct {
	PaymentID   string     `json:"payment_id"`
	OrderID     string     `json:"order_id"`
	Status      string     `json:"status"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type CreateOrderInput struct {
	UserID               string
	PlanID               string
	Region               string
	AcceptedTermsVersion string
	IdempotencyKey       string
	RequestHash          string
	Snapshot             PlanSnapshot
}

type CreatePaymentInput struct {
	UserID         string
	OrderID        string
	IdempotencyKey string
	RequestHash    string
}

type PaymentOperation struct {
	Payment
	UserID                 string
	PlanID                 string
	Provider               string
	ProviderPaymentID      *string
	ProviderIdempotencyKey string
	ProviderCreateDeadline time.Time
	NextReconcileAt        time.Time
	ReconcileAttempts      int
}

type WebhookNotification struct {
	InboxID          string
	EventType        string
	ProviderObjectID string
	ObservedStatus   string
	Attempts         int
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
	FindOrderReplay(context.Context, string, string, string) (Order, bool, error)
	CreateOrder(context.Context, CreateOrderInput) (Order, bool, error)
	GetOrder(context.Context, string, string) (Order, error)
	FindPaymentReplay(context.Context, string, string, string) (PaymentOperation, bool, error)
	CreatePayment(context.Context, CreatePaymentInput, string, time.Duration) (PaymentOperation, bool, error)
	GetPayment(context.Context, string) (PaymentOperation, error)
	GetPaymentByProviderID(context.Context, string) (PaymentOperation, error)
	PrepareProviderCreate(context.Context, string, time.Time) (PaymentOperation, bool, error)
	ApplyProviderCreate(context.Context, string, ProviderPayment) (PaymentOperation, error)
	MarkProviderCreateAmbiguous(context.Context, string, string, time.Duration) error
	MarkProviderCreateFailed(context.Context, string, string) error
	InsertWebhook(context.Context, WebhookNotification) (bool, error)
	ClaimWebhook(context.Context, time.Duration) (WebhookNotification, bool, error)
	CompleteWebhook(context.Context, string, string) error
	RejectWebhook(context.Context, string, string) error
	RetryWebhook(context.Context, string, string, time.Duration) error
	ClaimPaymentForReconcile(context.Context, time.Duration) (PaymentOperation, bool, error)
	ReschedulePayment(context.Context, string, string, time.Duration) error
	ApplyVerifiedPayment(context.Context, PaymentOperation, ProviderPayment, string) error
	ClaimOutbox(context.Context, time.Duration) (OutboxMessage, bool, error)
	CompleteOutbox(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
}

type ProviderPayment struct {
	ProviderPaymentID string
	Status            string
	AmountMinor       int64
	Currency          string
	ConfirmationURL   *string
	Test              bool
	AccountID         string
	MetadataOrderID   string
	MetadataPaymentID string
	CapturedAt        *time.Time
	CanceledAt        *time.Time
}

type Provider interface {
	CreatePayment(context.Context, ProviderCreateRequest) (ProviderPayment, error)
	GetPayment(context.Context, string) (ProviderPayment, error)
}

type ProviderCreateRequest struct {
	IdempotencyKey string
	OrderID        string
	PaymentID      string
	AmountMinor    int64
	Currency       string
}

type ProviderError struct {
	Kind string
	Code string
}

func (e *ProviderError) Error() string { return "payment provider request failed: " + e.Kind }

func IsProviderRetryable(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && (providerErr.Kind == "ambiguous" || providerErr.Kind == "temporary")
}

type IdentityVerifier interface {
	VerifyActiveWithConsent(context.Context, string, string) error
}

type Catalog interface {
	GetPlan(context.Context, string, string) (PlanSnapshot, error)
}
