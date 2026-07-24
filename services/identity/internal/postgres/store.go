package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/identity/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string, options ...platformpostgres.Option) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, dsn, options...)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) UpsertTelegramIdentity(ctx context.Context, profile domain.TelegramProfile) (domain.User, error) {
	if profile.TelegramUserID <= 0 {
		return domain.User{}, fmt.Errorf("telegram user id must be positive")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	newUserID, err := newUUID()
	if err != nil {
		return domain.User{}, err
	}

	_, err = tx.Exec(ctx, `
INSERT INTO users (id, status, locale, created_at, updated_at)
VALUES ($1, $2, $3, now(), now())`,
		newUserID,
		domain.UserStatusActive,
		nullableString(profile.Locale),
	)
	if err != nil {
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}

	var userID string
	err = tx.QueryRow(ctx, `
INSERT INTO telegram_identities (
    user_id,
    telegram_user_id,
    username,
    display_name,
    language_code,
    first_seen_at,
    last_seen_at
)
VALUES ($1, $2, $3, $4, $5, now(), now())
ON CONFLICT (telegram_user_id) DO UPDATE SET
    username = EXCLUDED.username,
    display_name = EXCLUDED.display_name,
    language_code = EXCLUDED.language_code,
    last_seen_at = now()
RETURNING user_id`,
		newUserID,
		profile.TelegramUserID,
		nullableString(profile.Username),
		nullableString(profile.DisplayName),
		nullableString(profile.LanguageCode),
	).Scan(&userID)
	if err != nil {
		return domain.User{}, fmt.Errorf("upsert telegram identity: %w", err)
	}

	if userID != newUserID {
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, newUserID); err != nil {
			return domain.User{}, fmt.Errorf("delete unused user: %w", err)
		}
		if profile.Locale != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET locale = $2, updated_at = now() WHERE id = $1`, userID, *profile.Locale); err != nil {
				return domain.User{}, fmt.Errorf("update user locale: %w", err)
			}
		}
	}

	user, err := getUserTx(ctx, tx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit transaction: %w", err)
	}
	return user, nil
}

func (s *Store) GetUser(ctx context.Context, userID string) (domain.User, error) {
	user, err := getUserQuerier(ctx, s.pool, userID)
	if err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) AcceptConsent(ctx context.Context, input domain.ConsentInput) error {
	consentID, err := newUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO consents (id, user_id, document_type, document_version, source, accepted_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (user_id, document_type, document_version) DO NOTHING`,
		consentID,
		input.UserID,
		input.DocumentType,
		input.DocumentVersion,
		input.Source,
	)
	if err != nil {
		return fmt.Errorf("accept consent: %w", err)
	}
	return nil
}

func (s *Store) HasConsent(ctx context.Context, userID, documentType, documentVersion string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM consents
    WHERE user_id = $1 AND document_type = $2 AND document_version = $3
)`, userID, documentType, documentVersion).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check consent: %w", err)
	}
	return exists, nil
}

func (s *Store) GetNotificationTarget(ctx context.Context, userID, documentType, documentVersion string) (domain.NotificationTarget, error) {
	var status string
	var telegramUserID sql.NullInt64
	var locale sql.NullString
	var consented bool
	err := s.pool.QueryRow(ctx, `
SELECT u.status, ti.telegram_user_id, u.locale,
       EXISTS (
           SELECT 1 FROM consents c
           WHERE c.user_id = u.id AND c.document_type = $2 AND c.document_version = $3
       )
FROM users u
LEFT JOIN telegram_identities ti ON ti.user_id = u.id
WHERE u.id = $1`, userID, documentType, documentVersion).Scan(&status, &telegramUserID, &locale, &consented)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationTarget{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.NotificationTarget{}, fmt.Errorf("get notification target: %w", err)
	}
	target := domain.NotificationTarget{Locale: nullStringPtr(locale)}
	switch {
	case status != domain.UserStatusActive:
		target.ReasonCode = "account_unavailable"
	case !telegramUserID.Valid || telegramUserID.Int64 <= 0:
		target.ReasonCode = "telegram_identity_missing"
	case !consented:
		target.ReasonCode = "consent_missing"
	default:
		target.Eligible = true
		target.TelegramChatID = &telegramUserID.Int64
	}
	return target, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getUserTx(ctx context.Context, tx pgx.Tx, userID string) (domain.User, error) {
	return getUserQuerier(ctx, tx, userID)
}

func getUserQuerier(ctx context.Context, q querier, userID string) (domain.User, error) {
	var user domain.User
	var locale sql.NullString
	var timezone sql.NullString
	err := q.QueryRow(ctx, `
SELECT id, status, locale, timezone
FROM users
WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&user.ID, &user.Status, &locale, &timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	user.Locale = stringPtrFromNull(locale)
	user.Timezone = stringPtrFromNull(timezone)
	return user, nil
}

func nullableString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func stringPtrFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
