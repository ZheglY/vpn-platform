package bot

import "context"

const (
	StateAwaitingConsent = "awaiting_consent"
	StateMenu            = "menu"

	DedupeAcquired   = "acquired"
	DedupeProcessing = "processing"
	DedupeCompleted  = "completed"
)

type DedupeStatus = string

type IdentityUser struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

type TelegramProfile struct {
	TelegramUserID int64
	Username       *string
	DisplayName    *string
	LanguageCode   *string
	Locale         *string
}

type IdentityClient interface {
	UpsertTelegramIdentity(ctx context.Context, profile TelegramProfile) (IdentityUser, error)
	HasConsent(ctx context.Context, userID, documentType, documentVersion string) (bool, error)
	AcceptConsent(ctx context.Context, userID, documentType, documentVersion string) error
}

type TelegramClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type Plan struct {
	PlanID       string   `json:"plan_id"`
	Name         string   `json:"name"`
	DurationDays int      `json:"duration_days"`
	AmountMinor  int64    `json:"amount_minor"`
	Currency     string   `json:"currency"`
	Regions      []string `json:"regions"`
}

type CatalogClient interface {
	ListPlans(context.Context) ([]Plan, error)
}

type Order struct {
	OrderID string `json:"order_id"`
}

type Payment struct {
	Status          string  `json:"status"`
	ConfirmationURL *string `json:"confirmation_url,omitempty"`
}

type BillingClient interface {
	CreateOrder(context.Context, string, string, string, string, string) (Order, error)
	CreatePayment(context.Context, string, string, string) (Payment, error)
}

type DedupeStore interface {
	StartProcessing(ctx context.Context, updateID int64, token string) (DedupeStatus, error)
	CompleteProcessing(ctx context.Context, updateID int64, token string) error
	ReleaseProcessing(ctx context.Context, updateID int64, token string) error
}

type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type FSMStore interface {
	GetState(ctx context.Context, telegramUserID int64) (string, error)
	SetState(ctx context.Context, telegramUserID int64, state string) error
}
