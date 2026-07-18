package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) ApplyPeriod(ctx context.Context, meta domain.EventMeta, event domain.PeriodEvent, seed domain.CredentialSeed) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin period event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, event.SubscriptionID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta)
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	now, err := transactionTime(ctx, tx)
	if err != nil {
		return err
	}

	var credentialID, userID, status string
	var revision int
	err = tx.QueryRow(ctx, `
SELECT id, user_id, status, credential_version
FROM access_credentials
WHERE subscription_id = $1 AND status <> 'revoked'
FOR UPDATE`, event.SubscriptionID).Scan(&credentialID, &userID, &status, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
INSERT INTO access_credentials (
    id, subscription_id, user_id, status, credential_version,
    vless_uuid_ciphertext, encryption_key_version, entitlement_expires_at, created_at, updated_at
) VALUES ($1,$2,$3,'provisioning',1,$4,$5,$6,$7,$7)`,
			seed.CredentialID, event.SubscriptionID, event.UserID, seed.Ciphertext, seed.KeyVersion, event.GraceEndsAt.UTC(), now); err != nil {
			return fmt.Errorf("insert access credential: %w", err)
		}
		if err := insertOperation(ctx, tx, seed.OperationID, seed.CredentialID, "provision", 1, meta.EventID, now); err != nil {
			return err
		}
		if err := insertOperationOutbox(ctx, tx, meta, now, "access.provision.request.v1", seed.OperationID, seed.CredentialID, 1); err != nil {
			return err
		}
		return commit(ctx, tx, "period event")
	}
	if err != nil {
		return fmt.Errorf("select current access credential: %w", err)
	}
	if userID != event.UserID {
		return domain.ErrDurableStateConflict
	}
	if status == domain.StatusRevoking || status == domain.StatusFailed {
		revision++
		if _, err := tx.Exec(ctx, `
UPDATE access_credentials
SET status = 'provisioning', credential_version = $2,
    entitlement_expires_at = GREATEST(entitlement_expires_at, $3),
    revoked_at = NULL, updated_at = $4
WHERE id = $1`, credentialID, revision, event.GraceEndsAt.UTC(), now); err != nil {
			return fmt.Errorf("restart access provisioning: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM access_endpoint_snapshots WHERE credential_id = $1`, credentialID); err != nil {
			return fmt.Errorf("clear stale endpoint snapshots: %w", err)
		}
		if err := insertOperation(ctx, tx, seed.OperationID, credentialID, "provision", revision, meta.EventID, now); err != nil {
			return err
		}
		if err := insertOperationOutbox(ctx, tx, meta, now, "access.provision.request.v1", seed.OperationID, credentialID, revision); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `
UPDATE access_credentials
SET entitlement_expires_at = GREATEST(entitlement_expires_at, $2), updated_at = $3
WHERE id = $1`, credentialID, event.GraceEndsAt.UTC(), now); err != nil {
			return fmt.Errorf("extend access entitlement: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE subscription_tokens
SET expires_at = GREATEST(expires_at, $2)
WHERE credential_id = $1 AND status = 'active'`, credentialID, event.GraceEndsAt.UTC()); err != nil {
			return fmt.Errorf("extend active token: %w", err)
		}
	}
	return commit(ctx, tx, "period event")
}

func (s *Store) ApplyTerminal(ctx context.Context, meta domain.EventMeta, event domain.TerminalEvent, operationID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin terminal event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, event.SubscriptionID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta)
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var credentialID, status string
	var revision int
	var entitlementExpires time.Time
	err = tx.QueryRow(ctx, `
SELECT id, status, credential_version, entitlement_expires_at
FROM access_credentials
WHERE subscription_id = $1 AND status <> 'revoked'
FOR UPDATE`, event.SubscriptionID).Scan(&credentialID, &status, &revision, &entitlementExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("select terminal access credential: %w", err)
	}
	if meta.EventType == "subscription.expired.v1" && event.EffectiveAt.Before(entitlementExpires) {
		return tx.Commit(ctx)
	}
	if status == domain.StatusRevoking {
		return tx.Commit(ctx)
	}
	now, err := transactionTime(ctx, tx)
	if err != nil {
		return err
	}
	revision++
	if _, err := tx.Exec(ctx, `
UPDATE access_credentials SET status = 'revoking', credential_version = $2, updated_at = $3 WHERE id = $1`, credentialID, revision, now); err != nil {
		return fmt.Errorf("mark credential revoking: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE subscription_tokens SET status = 'revoked', revoked_at = $2
WHERE credential_id = $1 AND status = 'active'`, credentialID, now); err != nil {
		return fmt.Errorf("revoke active tokens: %w", err)
	}
	if err := insertOperation(ctx, tx, operationID, credentialID, "revoke", revision, meta.EventID, now); err != nil {
		return err
	}
	if err := insertOperationOutbox(ctx, tx, meta, now, "access.revoke.request.v1", operationID, credentialID, revision); err != nil {
		return err
	}
	return commit(ctx, tx, "terminal event")
}

func (s *Store) ApplyProvisionSucceeded(ctx context.Context, meta domain.EventMeta, event domain.ProvisionSucceeded) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provisioning success: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, event.CredentialID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta)
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var subscriptionID, userID, credentialStatus, operationStatus string
	var revision int
	err = tx.QueryRow(ctx, `
SELECT c.subscription_id, c.user_id, c.status, c.credential_version, o.status
FROM access_credentials c JOIN access_operations o ON o.credential_id = c.id
WHERE c.id = $1 AND o.id = $2 AND o.kind = 'provision' AND o.desired_revision = $3
FOR UPDATE OF c, o`, event.CredentialID, event.OperationID, event.AppliedRevision).Scan(&subscriptionID, &userID, &credentialStatus, &revision, &operationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrDurableStateConflict
	}
	if err != nil {
		return fmt.Errorf("select provisioning operation: %w", err)
	}
	if revision != event.AppliedRevision || credentialStatus == domain.StatusRevoking || credentialStatus == domain.StatusRevoked {
		return tx.Commit(ctx)
	}
	if operationStatus != "pending" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM access_endpoint_snapshots WHERE credential_id = $1`, event.CredentialID); err != nil {
		return fmt.Errorf("replace endpoint snapshots: %w", err)
	}
	for _, endpoint := range event.Endpoints {
		if _, err := tx.Exec(ctx, `
INSERT INTO access_endpoint_snapshots (
    credential_id, node_id, role, address, port, server_name,
    reality_public_key, short_id, spider_x, label, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			event.CredentialID, endpoint.NodeID, endpoint.Role, endpoint.Address, endpoint.Port,
			endpoint.ServerName, endpoint.RealityPublicKey, endpoint.ShortID, endpoint.SpiderX, endpoint.Label, event.AppliedAt.UTC()); err != nil {
			return fmt.Errorf("insert endpoint snapshot: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE access_credentials SET status = $2, updated_at = $3 WHERE id = $1`, event.CredentialID, event.Status, event.AppliedAt.UTC()); err != nil {
		return fmt.Errorf("mark credential ready: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE access_operations SET status = 'succeeded', completed_at = $2 WHERE id = $1`, event.OperationID, event.AppliedAt.UTC()); err != nil {
		return fmt.Errorf("complete provisioning operation: %w", err)
	}
	readyData := map[string]any{
		"subscription_id": subscriptionID, "credential_id": event.CredentialID, "user_id": userID,
		"provisioning_status": event.Status, "ready_at": event.AppliedAt.UTC(), "link_issuance_required": true,
	}
	outboxTime, err := transactionTime(ctx, tx)
	if err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, meta, outboxTime, "access.ready.v1", "user:"+userID, event.CredentialID, "ready:"+event.OperationID, "access", readyData); err != nil {
		return err
	}
	return commit(ctx, tx, "provisioning success")
}

func (s *Store) ApplyOperationFailed(ctx context.Context, meta domain.EventMeta, event domain.OperationFailed, kind string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin operation failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, event.CredentialID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta)
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var operationStatus string
	var revision int
	err = tx.QueryRow(ctx, `
SELECT c.credential_version, o.status
FROM access_credentials c JOIN access_operations o ON o.credential_id = c.id
WHERE c.id = $1 AND o.id = $2 AND o.kind = $3 AND o.desired_revision = $4
FOR UPDATE OF c, o`, event.CredentialID, event.OperationID, kind, event.FailedRevision).Scan(&revision, &operationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrDurableStateConflict
	}
	if err != nil {
		return fmt.Errorf("select failed operation: %w", err)
	}
	if revision != event.FailedRevision || operationStatus != "pending" {
		return tx.Commit(ctx)
	}
	if event.Terminal {
		if _, err := tx.Exec(ctx, `UPDATE access_operations SET status = 'failed', failure_code = $2, completed_at = $3 WHERE id = $1`, event.OperationID, event.ReasonCode, event.FailedAt.UTC()); err != nil {
			return fmt.Errorf("fail access operation: %w", err)
		}
		if kind == "provision" {
			if _, err := tx.Exec(ctx, `UPDATE access_credentials SET status = 'failed', updated_at = $2 WHERE id = $1`, event.CredentialID, event.FailedAt.UTC()); err != nil {
				return fmt.Errorf("fail credential provisioning: %w", err)
			}
		}
	}
	return commit(ctx, tx, "operation failure")
}

func (s *Store) ApplyRevokeSucceeded(ctx context.Context, meta domain.EventMeta, event domain.RevokeSucceeded) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin revoke success: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, event.CredentialID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta)
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var operationStatus string
	var revision int
	err = tx.QueryRow(ctx, `
SELECT c.credential_version, o.status
FROM access_credentials c JOIN access_operations o ON o.credential_id = c.id
WHERE c.id = $1 AND o.id = $2 AND o.kind = 'revoke' AND o.desired_revision = $3
FOR UPDATE OF c, o`, event.CredentialID, event.OperationID, event.RevokedRevision).Scan(&revision, &operationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrDurableStateConflict
	}
	if err != nil {
		return fmt.Errorf("select revoke operation: %w", err)
	}
	if revision != event.RevokedRevision || operationStatus != "pending" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE access_credentials SET status = 'revoked', revoked_at = $2, updated_at = $2 WHERE id = $1`, event.CredentialID, event.RevokedAt.UTC()); err != nil {
		return fmt.Errorf("mark credential revoked: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE access_operations SET status = 'succeeded', completed_at = $2 WHERE id = $1`, event.OperationID, event.RevokedAt.UTC()); err != nil {
		return fmt.Errorf("complete revoke operation: %w", err)
	}
	return commit(ctx, tx, "revoke success")
}

func (s *Store) IssueToken(ctx context.Context, subscriptionID, operation string, seed domain.TokenSeed) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin URL issuance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAggregate(ctx, tx, subscriptionID); err != nil {
		return err
	}
	var existingSubscription, existingOperation, existingHash string
	err = tx.QueryRow(ctx, `SELECT subscription_id, operation, request_sha256 FROM url_idempotency WHERE idempotency_key = $1`, seed.IdempotencyKey).Scan(&existingSubscription, &existingOperation, &existingHash)
	if err == nil {
		if existingSubscription == subscriptionID && existingOperation == operation && existingHash == seed.RequestSHA256 {
			return domain.ErrIdempotencyReplay
		}
		return domain.ErrIdempotencyConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check URL idempotency: %w", err)
	}
	var credentialID, status string
	var expiresAt, now time.Time
	err = tx.QueryRow(ctx, `
SELECT id, status, entitlement_expires_at, now()
FROM access_credentials
WHERE subscription_id = $1 AND status <> 'revoked'
FOR UPDATE`, subscriptionID).Scan(&credentialID, &status, &expiresAt, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotReady
	}
	if err != nil {
		return fmt.Errorf("select credential for URL issuance: %w", err)
	}
	if (status != domain.StatusActive && status != domain.StatusDegraded) || !now.Before(expiresAt) {
		return domain.ErrNotReady
	}
	var previousTokenID string
	err = tx.QueryRow(ctx, `SELECT id FROM subscription_tokens WHERE credential_id = $1 AND status = 'active' FOR UPDATE`, credentialID).Scan(&previousTokenID)
	if operation == "issue" {
		if err == nil {
			return domain.ErrAlreadyIssued
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check active token: %w", err)
		}
	} else {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("select token for rotation: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE subscription_tokens SET status = 'rotated', revoked_at = $2 WHERE id = $1`, previousTokenID, now); err != nil {
			return fmt.Errorf("rotate previous token: %w", err)
		}
	}
	var rotatedFrom any
	if previousTokenID != "" {
		rotatedFrom = previousTokenID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO subscription_tokens (id, credential_id, token_lookup_hmac, status, expires_at, rotated_from, created_at)
VALUES ($1,$2,$3,'active',$4,$5,$6)`, seed.TokenID, credentialID, seed.LookupHMAC, expiresAt, rotatedFrom, now); err != nil {
		return fmt.Errorf("insert subscription token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO url_idempotency (idempotency_key, subscription_id, operation, request_sha256, token_id, completed_at)
VALUES ($1,$2,$3,$4,$5,$6)`, seed.IdempotencyKey, subscriptionID, operation, seed.RequestSHA256, seed.TokenID, now); err != nil {
		return fmt.Errorf("complete URL idempotency: %w", err)
	}
	return commit(ctx, tx, "URL issuance")
}

func (s *Store) GetAccessStatus(ctx context.Context, subscriptionID string) (domain.AccessStatus, error) {
	var credentialStatus, tokenStatus string
	err := s.pool.QueryRow(ctx, `
SELECT c.status, COALESCE((
    SELECT CASE WHEN t.status = 'active' AND t.expires_at <= now() THEN 'expired' ELSE t.status END
    FROM subscription_tokens t WHERE t.credential_id = c.id ORDER BY t.created_at DESC LIMIT 1
), 'not_issued')
FROM access_credentials c
WHERE c.subscription_id = $1
ORDER BY c.created_at DESC LIMIT 1`, subscriptionID).Scan(&credentialStatus, &tokenStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessStatus{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AccessStatus{}, fmt.Errorf("get access status: %w", err)
	}
	accessStatus := "pending"
	switch credentialStatus {
	case domain.StatusActive, domain.StatusDegraded:
		accessStatus = "ready"
		if tokenStatus == "active" {
			accessStatus = "active"
		}
	case domain.StatusFailed:
		accessStatus = "failed"
	case domain.StatusRevoking:
		accessStatus = "revoking"
	case domain.StatusRevoked:
		accessStatus = "revoked"
	}
	return domain.AccessStatus{SubscriptionID: subscriptionID, AccessStatus: accessStatus, ProvisioningStatus: credentialStatus, TokenStatus: tokenStatus}, nil
}

func (s *Store) GetProfileByTokenHMAC(ctx context.Context, lookup []byte) (domain.ProfileRecord, error) {
	var record domain.ProfileRecord
	var tokenID string
	err := s.pool.QueryRow(ctx, `
SELECT t.id, c.id, c.vless_uuid_ciphertext, c.encryption_key_version, c.entitlement_expires_at
FROM subscription_tokens t JOIN access_credentials c ON c.id = t.credential_id
WHERE t.token_lookup_hmac = $1 AND t.status = 'active'
  AND t.expires_at > now() AND c.entitlement_expires_at > now()
  AND c.status IN ('active', 'degraded')`, lookup).Scan(&tokenID, &record.CredentialID, &record.Ciphertext, &record.KeyVersion, &record.EntitlementExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProfileRecord{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProfileRecord{}, fmt.Errorf("lookup subscription token: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT node_id, role, address, port, server_name, reality_public_key, short_id, spider_x, label
FROM access_endpoint_snapshots WHERE credential_id = $1
ORDER BY CASE role WHEN 'primary' THEN 0 ELSE 1 END, node_id`, record.CredentialID)
	if err != nil {
		return domain.ProfileRecord{}, fmt.Errorf("list endpoint snapshots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var endpoint domain.EndpointSnapshot
		if err := rows.Scan(&endpoint.NodeID, &endpoint.Role, &endpoint.Address, &endpoint.Port, &endpoint.ServerName, &endpoint.RealityPublicKey, &endpoint.ShortID, &endpoint.SpiderX, &endpoint.Label); err != nil {
			return domain.ProfileRecord{}, fmt.Errorf("scan endpoint snapshot: %w", err)
		}
		record.Endpoints = append(record.Endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		return domain.ProfileRecord{}, fmt.Errorf("iterate endpoint snapshots: %w", err)
	}
	if len(record.Endpoints) == 0 {
		return domain.ProfileRecord{}, domain.ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE subscription_tokens SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes')`, tokenID)
	return record, nil
}

func (s *Store) GetProvisioningRecord(ctx context.Context, credentialID string) (domain.ProvisioningRecord, error) {
	var record domain.ProvisioningRecord
	err := s.pool.QueryRow(ctx, `
SELECT id, credential_version, vless_uuid_ciphertext, encryption_key_version
FROM access_credentials WHERE id = $1 AND status <> 'revoked'`, credentialID).Scan(&record.CredentialID, &record.Revision, &record.Ciphertext, &record.KeyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProvisioningRecord{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ProvisioningRecord{}, fmt.Errorf("get provisioning credential: %w", err)
	}
	return record, nil
}

func (s *Store) RecordDeadLetter(ctx context.Context, topic string, partition int32, offset int64, payloadSHA256, reason string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO consumer_dead_letters (topic, partition, record_offset, payload_sha256, reason_code)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (topic, partition, record_offset) DO NOTHING`, topic, partition, offset, payloadSHA256, reason)
	if err != nil {
		return fmt.Errorf("record access consumer dead letter: %w", err)
	}
	return nil
}

func (s *Store) ClaimOutbox(ctx context.Context, lease time.Duration) (domain.OutboxMessage, bool, error) {
	var message domain.OutboxMessage
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT o.event_id
    FROM outbox o
    WHERE o.state IN ('pending', 'processing')
      AND o.next_attempt_at <= now()
      AND (o.lease_until IS NULL OR o.lease_until <= now())
      AND NOT EXISTS (
          SELECT 1 FROM outbox older
          WHERE older.aggregate_id = o.aggregate_id AND older.state <> 'published'
            AND (older.created_at, older.event_id) < (o.created_at, o.event_id)
      )
    ORDER BY o.created_at, o.event_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE outbox o
SET state = 'processing', attempts = attempts + 1, lease_until = now() + $1::interval
FROM candidate WHERE o.event_id = candidate.event_id
RETURNING o.event_id, o.topic, o.partition_key, o.payload, o.attempts`, lease.String()).Scan(&message.EventID, &message.Topic, &message.PartitionKey, &message.Payload, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutboxMessage{}, false, nil
	}
	if err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("claim access outbox: %w", err)
	}
	return message, true, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, eventID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET state = 'published', published_at = now(), lease_until = NULL WHERE event_id = $1 AND state = 'processing'`, eventID)
	if err != nil {
		return fmt.Errorf("complete access outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrDurableStateConflict
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, eventID string, delay time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE outbox SET state = 'pending', next_attempt_at = now() + $2::interval, lease_until = NULL
WHERE event_id = $1 AND state = 'processing'`, eventID, delay.String())
	if err != nil {
		return fmt.Errorf("retry access outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrDurableStateConflict
	}
	return nil
}

func lockAggregate(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, id); err != nil {
		return fmt.Errorf("lock access aggregate: %w", err)
	}
	return nil
}

func insertInbox(ctx context.Context, tx pgx.Tx, meta domain.EventMeta) (bool, error) {
	var inserted int
	err := tx.QueryRow(ctx, `
INSERT INTO inbox (
    event_id, event_type, aggregate_id, correlation_id,
    source_topic, source_partition, source_offset, payload_sha256
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT DO NOTHING RETURNING 1`, meta.EventID, meta.EventType, meta.AggregateID, meta.CorrelationID,
		meta.SourceTopic, meta.SourcePartition, meta.SourceOffset, meta.PayloadSHA256).Scan(&inserted)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("insert access inbox: %w", err)
	}
	var eventID, eventType, payloadHash string
	err = tx.QueryRow(ctx, `
SELECT event_id, event_type, payload_sha256 FROM inbox
WHERE event_id = $1 OR (source_topic = $2 AND source_partition = $3 AND source_offset = $4)
LIMIT 1`, meta.EventID, meta.SourceTopic, meta.SourcePartition, meta.SourceOffset).Scan(&eventID, &eventType, &payloadHash)
	if err != nil {
		return false, fmt.Errorf("resolve access inbox conflict: %w", err)
	}
	if eventType != meta.EventType || payloadHash != meta.PayloadSHA256 {
		return false, domain.ErrDurableStateConflict
	}
	return false, nil
}

func insertOperation(ctx context.Context, tx pgx.Tx, operationID, credentialID, kind string, revision int, causationID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO access_operations (id, credential_id, kind, desired_revision, status, causation_event_id, created_at)
VALUES ($1,$2,$3,$4,'pending',$5,$6)`, operationID, credentialID, kind, revision, causationID, now); err != nil {
		return fmt.Errorf("insert access operation: %w", err)
	}
	return nil
}

func insertOperationOutbox(ctx context.Context, tx pgx.Tx, meta domain.EventMeta, now time.Time, topic, operationID, credentialID string, revision int) error {
	data := map[string]any{"operation_id": operationID, "credential_id": credentialID, "desired_revision": revision}
	return insertOutbox(ctx, tx, meta, now, topic, "credential:"+credentialID, credentialID, topic+":"+operationID, "credential", data)
}

func insertOutbox(ctx context.Context, tx pgx.Tx, meta domain.EventMeta, occurredAt time.Time, topic, partitionKey, aggregateID, dedupeKey, aggregateType string, data any) error {
	eventID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	causationID := meta.EventID
	envelope := platformkafka.Envelope{
		EventID: eventID, EventType: topic, SchemaVersion: 1, OccurredAt: occurredAt,
		Producer: "access-service", CorrelationID: meta.CorrelationID, CausationID: &causationID,
		AggregateType: aggregateType, AggregateID: aggregateID, PartitionKey: partitionKey,
	}
	envelope.Data, err = json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode access outbox data: %w", err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode access outbox envelope: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO outbox (event_id, topic, partition_key, aggregate_id, dedupe_key, payload, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, eventID, topic, partitionKey, aggregateID, dedupeKey, payload, occurredAt); err != nil {
		return fmt.Errorf("insert access outbox: %w", err)
	}
	return nil
}

func transactionTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read transaction time: %w", err)
	}
	return now.UTC(), nil
}

func commit(ctx context.Context, tx pgx.Tx, operation string) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}
