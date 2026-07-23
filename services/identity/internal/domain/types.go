package domain

import (
	"context"
	"errors"
)

const (
	UserStatusActive  = "active"
	UserStatusBlocked = "blocked"
	UserStatusDeleted = "deleted"

	ConsentTerms = "terms"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID       string
	Status   string
	Locale   *string
	Timezone *string
}

type TelegramProfile struct {
	TelegramUserID int64
	Username       *string
	DisplayName    *string
	LanguageCode   *string
	Locale         *string
}

type ConsentInput struct {
	UserID          string
	DocumentType    string
	DocumentVersion string
	Source          string
}

type NotificationTarget struct {
	Eligible       bool
	ReasonCode     string
	TelegramChatID *int64
	Locale         *string
}

type Store interface {
	UpsertTelegramIdentity(ctx context.Context, profile TelegramProfile) (User, error)
	GetUser(ctx context.Context, userID string) (User, error)
	AcceptConsent(ctx context.Context, input ConsentInput) error
	HasConsent(ctx context.Context, userID, documentType, documentVersion string) (bool, error)
	GetNotificationTarget(ctx context.Context, userID, documentType, documentVersion string) (NotificationTarget, error)
	Ping(ctx context.Context) error
	Close()
}
