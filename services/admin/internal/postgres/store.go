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
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
	"github.com/ZheglY/vpn-platform/services/admin/internal/rbac"
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

func (s *Store) GetPrincipal(ctx context.Context, spiffeID, name string) (domain.Principal, error) {
	rows, err := s.pool.Query(ctx, `
SELECT g.role
FROM admin_principals p JOIN admin_role_grants g ON g.spiffe_id = p.spiffe_id
WHERE p.spiffe_id = $1 AND p.enabled
ORDER BY g.role`, spiffeID)
	if err != nil {
		return domain.Principal{}, fmt.Errorf("query admin principal: %w", err)
	}
	defer rows.Close()
	principal := domain.Principal{SPIFFEID: spiffeID, Name: name}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return domain.Principal{}, fmt.Errorf("scan admin role: %w", err)
		}
		principal.Roles = append(principal.Roles, role)
	}
	if err := rows.Err(); err != nil {
		return domain.Principal{}, fmt.Errorf("iterate admin roles: %w", err)
	}
	if len(principal.Roles) == 0 {
		return domain.Principal{}, domain.ErrForbidden
	}
	permissions, ok := rbac.Permissions(principal.Roles)
	if !ok {
		return domain.Principal{}, domain.ErrForbidden
	}
	principal.Permissions = permissions
	return principal, nil
}

func (s *Store) BeginAction(ctx context.Context, input domain.ActionInput) (domain.Action, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("begin admin action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := input.Actor.SPIFFEID + "\n" + input.Action + "\n" + input.IdempotencyKeySHA256
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return domain.Action{}, false, fmt.Errorf("lock admin action: %w", err)
	}
	action, storedHash, storedReason, err := readActionByIdempotency(ctx, tx, input.Actor.SPIFFEID, input.Action, input.IdempotencyKeySHA256)
	if err == nil {
		if storedHash != input.RequestSHA256 || action.TargetType != input.TargetType || action.TargetID != input.TargetID || storedReason != input.Reason {
			return domain.Action{}, false, domain.ErrIdempotencyConflict
		}
		action.Replay = true
		return action, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Action{}, false, fmt.Errorf("read admin action replay: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO admin_action_requests (
    action_id, actor_spiffe_id, actor_principal, action, permission, target_type, target_id,
    reason, reason_code, idempotency_key_sha256, request_sha256, request_id,
    correlation_id, roles_snapshot, permissions_snapshot, status
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,'pending')`,
		input.ActionID, input.Actor.SPIFFEID, input.Actor.Name, input.Action, input.Permission, input.TargetType, input.TargetID,
		input.Reason, input.ReasonCode, input.IdempotencyKeySHA256, input.RequestSHA256, input.RequestID,
		input.CorrelationID, input.Actor.Roles, input.Actor.Permissions)
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("insert admin action: %w", err)
	}
	if err := insertAudit(ctx, tx, input, "accepted", "", nil); err != nil {
		return domain.Action{}, false, err
	}
	action, _, _, err = readActionByID(ctx, tx, input.ActionID)
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("read accepted admin action: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Action{}, false, fmt.Errorf("commit accepted admin action: %w", err)
	}
	return action, false, nil
}

func (s *Store) StartActionAttempt(ctx context.Context, actionID, requestID string, lease time.Duration) (domain.Action, bool, error) {
	claimID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("generate admin action claim: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("begin admin action attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action, _, _, err := readActionByIDForUpdate(ctx, tx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Action{}, false, domain.ErrNotFound
	}
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("lock admin action attempt: %w", err)
	}
	if action.Status == "succeeded" || action.Status == "failed" {
		action.Replay = true
		return action, false, tx.Commit(ctx)
	}
	if action.Status == "processing" {
		var leaseActive bool
		if err := tx.QueryRow(ctx, `
SELECT lease_until > clock_timestamp()
FROM admin_action_requests WHERE action_id = $1`, actionID).Scan(&leaseActive); err != nil {
			return domain.Action{}, false, fmt.Errorf("check admin action lease: %w", err)
		}
		if leaseActive {
			action.Replay = true
			return action, false, tx.Commit(ctx)
		}
	}
	outcome := "attempted"
	if action.Attempts > 0 {
		outcome = "retrying"
	}
	tag, err := tx.Exec(ctx, `
UPDATE admin_action_requests
SET status = 'processing', attempts = attempts + 1, claim_id = $2,
    lease_until = clock_timestamp() + $3::interval, last_attempt_at = clock_timestamp(),
    error_code = NULL
WHERE action_id = $1
  AND (status IN ('pending', 'outcome_unknown')
       OR (status = 'processing' AND lease_until <= clock_timestamp()))`,
		actionID, claimID, lease.String())
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("claim admin action attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Action{}, false, domain.ErrActionStateConflict
	}
	input, err := actionInputForAudit(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, false, err
	}
	input.RequestID = requestID
	if err := insertAudit(ctx, tx, input, outcome, "", nil); err != nil {
		return domain.Action{}, false, err
	}
	action, _, _, err = readActionByID(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, false, fmt.Errorf("read claimed admin action: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Action{}, false, fmt.Errorf("commit admin action attempt: %w", err)
	}
	return action, true, nil
}

func (s *Store) CompleteAction(ctx context.Context, actionID, claimID, requestID string, result json.RawMessage, errorCode string) (domain.Action, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Action{}, fmt.Errorf("begin complete admin action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action, _, _, err := readActionByIDForUpdate(ctx, tx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Action{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Action{}, fmt.Errorf("lock admin action: %w", err)
	}
	if action.Status == "succeeded" || action.Status == "failed" {
		action.Replay = true
		return action, tx.Commit(ctx)
	}
	if action.Status != "processing" || action.ClaimID != claimID {
		return domain.Action{}, domain.ErrActionStateConflict
	}
	status, outcome := "succeeded", "succeeded"
	var resultValue any = string(result)
	if errorCode != "" {
		status, outcome, resultValue = "failed", "failed", nil
	}
	tag, err := tx.Exec(ctx, `
UPDATE admin_action_requests
SET status = $2, result = $3, error_code = NULLIF($4,''), completed_at = clock_timestamp(),
    claim_id = NULL, lease_until = NULL
WHERE action_id = $1 AND status = 'processing' AND claim_id = $5`,
		actionID, status, resultValue, errorCode, claimID)
	if err != nil {
		return domain.Action{}, fmt.Errorf("complete admin action: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Action{}, domain.ErrActionStateConflict
	}
	input, err := actionInputForAudit(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, err
	}
	input.RequestID = requestID
	completedAt := time.Now().UTC()
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&completedAt); err != nil {
		return domain.Action{}, fmt.Errorf("read admin completion time: %w", err)
	}
	if err := insertAudit(ctx, tx, input, outcome, errorCode, &completedAt); err != nil {
		return domain.Action{}, err
	}
	action, _, _, err = readActionByID(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read completed admin action: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Action{}, fmt.Errorf("commit completed admin action: %w", err)
	}
	return action, nil
}

func (s *Store) MarkActionOutcomeUnknown(ctx context.Context, actionID, claimID, requestID, errorCode string) (domain.Action, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Action{}, fmt.Errorf("begin unknown admin outcome: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action, _, _, err := readActionByIDForUpdate(ctx, tx, actionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Action{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Action{}, fmt.Errorf("lock unknown admin outcome: %w", err)
	}
	if action.Status == "succeeded" || action.Status == "failed" {
		action.Replay = true
		return action, tx.Commit(ctx)
	}
	if action.Status != "processing" || action.ClaimID != claimID {
		return domain.Action{}, domain.ErrActionStateConflict
	}
	tag, err := tx.Exec(ctx, `
UPDATE admin_action_requests
SET status = 'outcome_unknown', error_code = $3, claim_id = NULL, lease_until = NULL
WHERE action_id = $1 AND status = 'processing' AND claim_id = $2`,
		actionID, claimID, errorCode)
	if err != nil {
		return domain.Action{}, fmt.Errorf("mark unknown admin outcome: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Action{}, domain.ErrActionStateConflict
	}
	input, err := actionInputForAudit(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, err
	}
	input.RequestID = requestID
	if err := insertAudit(ctx, tx, input, "outcome_unknown", errorCode, nil); err != nil {
		return domain.Action{}, err
	}
	action, _, _, err = readActionByID(ctx, tx, actionID)
	if err != nil {
		return domain.Action{}, fmt.Errorf("read unknown admin outcome: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Action{}, fmt.Errorf("commit unknown admin outcome: %w", err)
	}
	return action, nil
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT audit_event_id, action_id, actor_principal, verified_spiffe_identity,
       roles_snapshot, permissions_snapshot, action, permission, target_type,
       target_id, reason, outcome, error_code, request_id, correlation_id,
       occurred_at, completed_at
FROM admin_audit_events
ORDER BY occurred_at DESC, audit_event_id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	var events []domain.AuditEvent
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.AuditEventID, &event.ActionID, &event.ActorPrincipal, &event.VerifiedSPIFFEIdentity,
			&event.Roles, &event.Permissions, &event.Action, &event.Permission, &event.TargetType,
			&event.TargetID, &event.Reason, &event.Outcome, &event.ErrorCode, &event.RequestID, &event.CorrelationID,
			&event.OccurredAt, &event.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan admin audit: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readActionByIdempotency(ctx context.Context, query querier, actor, action, keyHash string) (domain.Action, string, string, error) {
	return scanAction(query.QueryRow(ctx, `
SELECT action_id, action, target_type, target_id, status, result, error_code,
       correlation_id, created_at, completed_at, attempts,
       COALESCE(claim_id, '00000000-0000-0000-0000-000000000000'::uuid),
       lease_until, request_sha256, reason
FROM admin_action_requests
WHERE actor_spiffe_id = $1 AND action = $2 AND idempotency_key_sha256 = $3`, actor, action, keyHash))
}

func readActionByID(ctx context.Context, query querier, actionID string) (domain.Action, string, string, error) {
	return scanAction(query.QueryRow(ctx, `
SELECT action_id, action, target_type, target_id, status, result, error_code,
       correlation_id, created_at, completed_at, attempts,
       COALESCE(claim_id, '00000000-0000-0000-0000-000000000000'::uuid),
       lease_until, request_sha256, reason
FROM admin_action_requests WHERE action_id = $1`, actionID))
}

func readActionByIDForUpdate(ctx context.Context, query querier, actionID string) (domain.Action, string, string, error) {
	return scanAction(query.QueryRow(ctx, `
SELECT action_id, action, target_type, target_id, status, result, error_code,
       correlation_id, created_at, completed_at, attempts,
       COALESCE(claim_id, '00000000-0000-0000-0000-000000000000'::uuid),
       lease_until, request_sha256, reason
FROM admin_action_requests WHERE action_id = $1 FOR UPDATE`, actionID))
}

func scanAction(row pgx.Row) (domain.Action, string, string, error) {
	var action domain.Action
	var result []byte
	var requestHash, reason string
	err := row.Scan(&action.ActionID, &action.Action, &action.TargetType, &action.TargetID, &action.Status,
		&result, &action.ErrorCode, &action.CorrelationID, &action.CreatedAt, &action.CompletedAt,
		&action.Attempts, &action.ClaimID, &action.LeaseUntil, &requestHash, &reason)
	if len(result) > 0 {
		action.Result = json.RawMessage(result)
	}
	return action, requestHash, reason, err
}

func actionInputForAudit(ctx context.Context, query querier, actionID string) (domain.ActionInput, error) {
	var input domain.ActionInput
	err := query.QueryRow(ctx, `
SELECT a.action_id, a.actor_spiffe_id, a.actor_principal, a.roles_snapshot,
       a.permissions_snapshot, a.action, a.permission, a.target_type, a.target_id,
       a.reason, COALESCE(a.reason_code,''), a.idempotency_key_sha256,
       a.request_sha256, a.request_id, a.correlation_id
FROM admin_action_requests a
WHERE a.action_id = $1`, actionID).Scan(&input.ActionID, &input.Actor.SPIFFEID, &input.Actor.Name,
		&input.Actor.Roles, &input.Actor.Permissions, &input.Action, &input.Permission, &input.TargetType,
		&input.TargetID, &input.Reason, &input.ReasonCode, &input.IdempotencyKeySHA256,
		&input.RequestSHA256, &input.RequestID, &input.CorrelationID)
	if err != nil {
		return domain.ActionInput{}, fmt.Errorf("read action audit snapshot: %w", err)
	}
	return input, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, input domain.ActionInput, outcome, errorCode string, completedAt *time.Time) error {
	auditID, err := cryptoutil.RandomUUID()
	if err != nil {
		return fmt.Errorf("generate audit event id: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO admin_audit_events (
    audit_event_id, action_id, actor_principal, verified_spiffe_identity,
    roles_snapshot, permissions_snapshot, action, permission, target_type, target_id,
    reason, idempotency_key_sha256, request_id, correlation_id, outcome, error_code,
    completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,''),$17)`,
		auditID, input.ActionID, input.Actor.Name, input.Actor.SPIFFEID, input.Actor.Roles,
		input.Actor.Permissions, input.Action, input.Permission, input.TargetType, input.TargetID,
		input.Reason, input.IdempotencyKeySHA256, input.RequestID, input.CorrelationID,
		outcome, errorCode, completedAt)
	if err != nil {
		return fmt.Errorf("insert admin audit event: %w", err)
	}
	return nil
}
