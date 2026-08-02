package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string, options ...platformpostgres.Option) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, databaseURL, options...)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) RecordEvent(ctx context.Context, meta domain.EventMeta, intent domain.Intent) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin notification event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, intent.DeliveryStreamKey); err != nil {
		return false, fmt.Errorf("lock notification delivery stream: %w", err)
	}

	var existingHash string
	err = tx.QueryRow(ctx, `
SELECT payload_sha256 FROM notification_inbox WHERE event_id = $1`, meta.EventID).Scan(&existingHash)
	if err == nil {
		if existingHash != meta.PayloadSHA256 {
			return false, domain.ErrEventConflict
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("lookup notification inbox: %w", err)
	}

	var sequence any
	if meta.AggregateSequence > 0 {
		sequence = meta.AggregateSequence
		streamKey := meta.Producer + ":" + meta.AggregateType + ":" + meta.AggregateID
		var last int64
		err = tx.QueryRow(ctx, `SELECT last_sequence FROM notification_cursors WHERE stream_key = $1 FOR UPDATE`, streamKey).Scan(&last)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && meta.AggregateSequence != 1:
			return false, domain.ErrSequenceGap
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx, `
INSERT INTO notification_cursors (
    stream_key, producer, aggregate_type, aggregate_id,
    last_sequence, last_event_id, last_payload_sha256
) VALUES ($1, $2, $3, $4, $5, $6, $7)`, streamKey, meta.Producer, meta.AggregateType, meta.AggregateID,
				meta.AggregateSequence, meta.EventID, meta.PayloadSHA256)
		case err != nil:
			return false, fmt.Errorf("lock notification cursor: %w", err)
		case meta.AggregateSequence == last+1:
			_, err = tx.Exec(ctx, `
UPDATE notification_cursors
SET last_sequence = $2, last_event_id = $3, last_payload_sha256 = $4, updated_at = clock_timestamp()
WHERE stream_key = $1`, streamKey, meta.AggregateSequence, meta.EventID, meta.PayloadSHA256)
		case meta.AggregateSequence > last+1:
			return false, domain.ErrSequenceGap
		default:
			return false, domain.ErrEventConflict
		}
		if err != nil {
			return false, fmt.Errorf("advance notification cursor: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
INSERT INTO notification_inbox (
    event_id, event_type, source_topic, source_partition, source_offset,
    payload_sha256, producer, aggregate_type, aggregate_id, aggregate_sequence, business_dedupe_key
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		meta.EventID, meta.EventType, meta.SourceTopic, meta.SourcePartition, meta.SourceOffset,
		meta.PayloadSHA256, meta.Producer, meta.AggregateType, meta.AggregateID, sequence, intent.BusinessDedupeKey)
	if err != nil {
		if isUniqueViolation(err) {
			return false, domain.ErrEventConflict
		}
		return false, fmt.Errorf("insert notification inbox: %w", err)
	}

	variables, err := json.Marshal(intent.Variables)
	if err != nil {
		return false, fmt.Errorf("marshal notification variables: %w", err)
	}
	status, reason := domain.StatusPending, any(nil)
	if intent.SuppressedReason != "" {
		status, reason = domain.StatusSuppressed, intent.SuppressedReason
	} else {
		var superseded bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM notification_jobs
    WHERE delivery_stream_key = $1
      AND delivery_sequence > $2
      AND supersedes_predecessors
)`, intent.DeliveryStreamKey, intent.DeliverySequence).Scan(&superseded); err != nil {
			return false, fmt.Errorf("check notification stream superseder: %w", err)
		}
		if superseded {
			status, reason = domain.StatusSuppressed, "superseded_by_terminal_state"
		}
	}
	result, err := tx.Exec(ctx, `
INSERT INTO notification_jobs (
    notification_id, user_id, subscription_id, credential_id, notification_type,
    template_version, source_event_id, business_dedupe_key, status, max_attempts,
    terminal_reason_code, correlation_id, causation_id, variables,
    source_producer, source_aggregate_type, source_aggregate_id, source_aggregate_sequence,
    delivery_stream_key, delivery_sequence, supersedes_predecessors
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (business_dedupe_key) DO NOTHING`,
		intent.NotificationID, intent.UserID, intent.SubscriptionID, intent.CredentialID, intent.NotificationType,
		intent.TemplateVersion, meta.EventID, intent.BusinessDedupeKey, status, intent.MaxAttempts,
		reason, meta.CorrelationID, meta.CausationID, variables,
		meta.Producer, meta.AggregateType, meta.AggregateID, sequence,
		intent.DeliveryStreamKey, intent.DeliverySequence, intent.SupersedesOlder)
	if err != nil {
		return false, fmt.Errorf("insert notification job: %w", err)
	}
	if intent.SupersedesOlder {
		if _, err := tx.Exec(ctx, `
UPDATE notification_jobs
SET status = 'suppressed', terminal_reason_code = 'superseded_by_terminal_state',
    lease_until = NULL, claim_id = NULL, updated_at = clock_timestamp()
WHERE delivery_stream_key = $1
  AND delivery_sequence < $2
  AND status IN ('pending', 'retry')`,
			intent.DeliveryStreamKey, intent.DeliverySequence); err != nil {
			return false, fmt.Errorf("suppress notification stream predecessors: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit notification event: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) RecordDeadLetter(ctx context.Context, topic string, partition int32, offset int64, payloadHash, eventType, reason string) error {
	result, err := s.pool.Exec(ctx, `
INSERT INTO notification_dead_letters (
    source_topic, source_partition, source_offset, payload_sha256, event_type, reason_code
) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)
ON CONFLICT (source_topic, source_partition, source_offset) DO NOTHING`, topic, partition, offset, payloadHash, eventType, reason)
	if err != nil {
		return fmt.Errorf("record notification dead letter: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var storedHash, storedReason string
	var storedEventType *string
	if err := s.pool.QueryRow(ctx, `
SELECT payload_sha256, event_type, reason_code FROM notification_dead_letters
WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3`, topic, partition, offset).Scan(&storedHash, &storedEventType, &storedReason); err != nil {
		return fmt.Errorf("resolve notification dead letter conflict: %w", err)
	}
	storedType := ""
	if storedEventType != nil {
		storedType = *storedEventType
	}
	if storedHash != payloadHash || storedType != eventType || storedReason != reason {
		return domain.ErrEventConflict
	}
	return nil
}

func (s *Store) ListDeadLetters(ctx context.Context, limit int) ([]domain.DeadLetter, error) {
	rows, err := s.pool.Query(ctx, `
SELECT source_topic, source_partition, source_offset, payload_sha256, event_type,
       reason_code, state, created_at, updated_at
FROM notification_dead_letters
ORDER BY created_at DESC, source_topic, source_partition, source_offset
LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification dead letters: %w", err)
	}
	defer rows.Close()
	var deadLetters []domain.DeadLetter
	for rows.Next() {
		var item domain.DeadLetter
		if err := rows.Scan(&item.SourceTopic, &item.SourcePartition, &item.SourceOffset, &item.PayloadSHA256,
			&item.EventType, &item.ReasonCode, &item.State, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification dead letter: %w", err)
		}
		deadLetters = append(deadLetters, item)
	}
	return deadLetters, rows.Err()
}

func (s *Store) ClaimJob(ctx context.Context, lease time.Duration) (domain.Job, bool, error) {
	claimID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("generate notification claim: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT notification_id
    FROM notification_jobs candidate_job
    WHERE (
        (status IN ('pending','retry') AND next_attempt_at <= clock_timestamp())
        OR (status = 'processing' AND lease_until <= clock_timestamp())
    )
    AND NOT EXISTS (
        SELECT 1
        FROM notification_jobs predecessor
        WHERE predecessor.delivery_stream_key = candidate_job.delivery_stream_key
          AND predecessor.delivery_sequence < candidate_job.delivery_sequence
          AND predecessor.status IN ('pending', 'retry', 'processing')
    )
    ORDER BY next_attempt_at, delivery_stream_key, delivery_sequence, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), claimed AS (
    UPDATE notification_jobs j
    SET status = 'processing', attempts = attempts + 1,
        claim_id = $1, lease_until = clock_timestamp() + $2::interval,
        updated_at = clock_timestamp()
    FROM candidate c
    WHERE j.notification_id = c.notification_id
    RETURNING j.*
)
SELECT notification_id, user_id, subscription_id, credential_id, notification_type,
       template_version, status, attempts, max_attempts, next_attempt_at, claim_id,
       source_producer, source_aggregate_type, source_aggregate_id, COALESCE(source_aggregate_sequence, 0),
       delivery_stream_key, delivery_sequence, supersedes_predecessors,
       correlation_id, causation_id, variables, terminal_reason_code, delivered_at,
       created_at, updated_at
FROM claimed`, claimID, lease.String())
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("claim notification job: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteJob(ctx context.Context, notificationID, claimID string) error {
	return s.finishClaim(ctx, notificationID, claimID, domain.StatusDelivered, "", 0)
}

func (s *Store) RetryJob(ctx context.Context, notificationID, claimID string, delay time.Duration) error {
	return s.finishClaim(ctx, notificationID, claimID, domain.StatusRetry, "", delay)
}

func (s *Store) FailJob(ctx context.Context, notificationID, claimID, reason string) error {
	return s.finishClaim(ctx, notificationID, claimID, domain.StatusPermanentlyFailed, reason, 0)
}

func (s *Store) SuppressJob(ctx context.Context, notificationID, claimID, reason string) error {
	return s.finishClaim(ctx, notificationID, claimID, domain.StatusSuppressed, reason, 0)
}

func (s *Store) finishClaim(ctx context.Context, notificationID, claimID, status, reason string, delay time.Duration) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin finish notification claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var streamKey string
	var deliverySequence int64
	err = tx.QueryRow(ctx, `
SELECT delivery_stream_key, delivery_sequence
FROM notification_jobs
WHERE notification_id = $1 AND status = 'processing' AND claim_id = $2
FOR UPDATE`, notificationID, claimID).Scan(&streamKey, &deliverySequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrEventConflict
	}
	if err != nil {
		return fmt.Errorf("lock notification claim: %w", err)
	}
	if status == domain.StatusRetry || status == domain.StatusPermanentlyFailed {
		var superseded bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM notification_jobs
    WHERE delivery_stream_key = $1
      AND delivery_sequence > $2
      AND supersedes_predecessors
)`, streamKey, deliverySequence).Scan(&superseded); err != nil {
			return fmt.Errorf("check notification claim superseder: %w", err)
		}
		if superseded {
			status, reason, delay = domain.StatusSuppressed, "superseded_by_terminal_state", 0
		}
	}
	result, err := tx.Exec(ctx, `
UPDATE notification_jobs
SET status = $3,
    next_attempt_at = CASE WHEN $3 = 'retry' THEN clock_timestamp() + $5::interval ELSE next_attempt_at END,
    delivered_at = CASE WHEN $3 = 'delivered' THEN clock_timestamp() ELSE NULL END,
    terminal_reason_code = NULLIF($4,''), lease_until = NULL, claim_id = NULL,
    updated_at = clock_timestamp()
WHERE notification_id = $1 AND status = 'processing' AND claim_id = $2`, notificationID, claimID, status, reason, delay.String())
	if err != nil {
		return fmt.Errorf("finish notification claim: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrEventConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit notification claim: %w", err)
	}
	return nil
}

func (s *Store) GetJob(ctx context.Context, notificationID string) (domain.Job, error) {
	job, err := scanJob(s.pool.QueryRow(ctx, `
SELECT notification_id, user_id, subscription_id, credential_id, notification_type,
       template_version, status, attempts, max_attempts, next_attempt_at, COALESCE(claim_id, '00000000-0000-0000-0000-000000000000'::uuid),
       source_producer, source_aggregate_type, source_aggregate_id, COALESCE(source_aggregate_sequence, 0),
       delivery_stream_key, delivery_sequence, supersedes_predecessors,
       correlation_id, causation_id, variables, terminal_reason_code, delivered_at,
       created_at, updated_at
FROM notification_jobs WHERE notification_id = $1`, notificationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Job{}, fmt.Errorf("get notification job: %w", err)
	}
	return job, nil
}

func (s *Store) RequestRetry(ctx context.Context, notificationID, idempotencyKey, requestHash string) (domain.Job, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("begin notification retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existingHash, existingNotificationID string
	err = tx.QueryRow(ctx, `SELECT request_sha256, notification_id FROM notification_admin_requests WHERE idempotency_key = $1`, idempotencyKey).Scan(&existingHash, &existingNotificationID)
	if err == nil {
		if existingHash != requestHash || existingNotificationID != notificationID {
			return domain.Job{}, false, domain.ErrIdempotencyConflict
		}
		job, err := getJobQuery(ctx, tx, notificationID)
		return job, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, false, fmt.Errorf("lookup notification retry: %w", err)
	}
	var status, streamKey string
	var deliverySequence int64
	err = tx.QueryRow(ctx, `
SELECT status, delivery_stream_key, delivery_sequence
FROM notification_jobs WHERE notification_id = $1 FOR UPDATE`, notificationID).Scan(&status, &streamKey, &deliverySequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, false, domain.ErrNotFound
	}
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("lock notification job: %w", err)
	}
	if status != domain.StatusPermanentlyFailed && status != domain.StatusRetry {
		return domain.Job{}, false, domain.ErrRetryNotAllowed
	}
	var successorExists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM notification_jobs
    WHERE delivery_stream_key = $1 AND delivery_sequence > $2
)`, streamKey, deliverySequence).Scan(&successorExists); err != nil {
		return domain.Job{}, false, fmt.Errorf("check notification retry ordering: %w", err)
	}
	if successorExists {
		return domain.Job{}, false, domain.ErrRetryNotAllowed
	}
	_, err = tx.Exec(ctx, `
UPDATE notification_jobs SET status = 'pending', attempts = 0, next_attempt_at = clock_timestamp(),
    lease_until = NULL, claim_id = NULL, terminal_reason_code = NULL, updated_at = clock_timestamp()
WHERE notification_id = $1`, notificationID)
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("reset notification job: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO notification_admin_requests (idempotency_key, request_sha256, notification_id, result_status)
VALUES ($1,$2,$3,'accepted')`, idempotencyKey, requestHash, notificationID)
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("insert notification retry request: %w", err)
	}
	job, err := getJobQuery(ctx, tx, notificationID)
	if err != nil {
		return domain.Job{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Job{}, false, fmt.Errorf("commit notification retry: %w", err)
	}
	return job, false, nil
}

type rowScanner interface{ Scan(...any) error }

func scanJob(row rowScanner) (domain.Job, error) {
	var job domain.Job
	var variables []byte
	err := row.Scan(&job.NotificationID, &job.UserID, &job.SubscriptionID, &job.CredentialID,
		&job.NotificationType, &job.TemplateVersion, &job.Status, &job.Attempts, &job.MaxAttempts,
		&job.NextAttemptAt, &job.ClaimID, &job.SourceProducer, &job.SourceAggregate, &job.SourceAggregateID, &job.SourceSequence,
		&job.DeliveryStream, &job.DeliverySequence, &job.SupersedesOlder,
		&job.CorrelationID, &job.CausationID, &variables,
		&job.TerminalReason, &job.DeliveredAt, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return domain.Job{}, err
	}
	if err := json.Unmarshal(variables, &job.Variables); err != nil {
		return domain.Job{}, fmt.Errorf("decode notification variables: %w", err)
	}
	return job, nil
}

func getJobQuery(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, notificationID string) (domain.Job, error) {
	job, err := scanJob(query.QueryRow(ctx, `
SELECT notification_id, user_id, subscription_id, credential_id, notification_type,
       template_version, status, attempts, max_attempts, next_attempt_at,
       COALESCE(claim_id, '00000000-0000-0000-0000-000000000000'::uuid),
       source_producer, source_aggregate_type, source_aggregate_id, COALESCE(source_aggregate_sequence, 0),
       delivery_stream_key, delivery_sequence, supersedes_predecessors,
       correlation_id, causation_id, variables, terminal_reason_code, delivered_at,
       created_at, updated_at
FROM notification_jobs WHERE notification_id = $1`, notificationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrNotFound
	}
	return job, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
