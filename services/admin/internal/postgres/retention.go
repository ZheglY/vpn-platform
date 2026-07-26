package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
)

func (s *Store) RetentionClock(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read admin retention clock: %w", err)
	}
	return now.UTC(), nil
}

func (s *Store) RetentionDatasets(keepFor time.Duration) []retention.Dataset {
	return []retention.Dataset{{
		Name:    "admin_audit_events",
		KeepFor: keepFor,
		Preview: func(ctx context.Context, cutoff time.Time) (retention.Eligibility, error) {
			var count int64
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events WHERE occurred_at < $1`, cutoff).Scan(&count); err != nil {
				return retention.Eligibility{}, fmt.Errorf("preview admin audit retention: %w", err)
			}
			return retention.Eligibility{Eligible: count}, nil
		},
		DeleteBatch: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
			tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				return 0, fmt.Errorf("begin admin audit retention: %w", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `SET LOCAL vpn.admin_audit_retention = 'on'`); err != nil {
				return 0, fmt.Errorf("authorize admin audit retention: %w", err)
			}
			tag, err := tx.Exec(ctx, `
WITH selected AS (
    SELECT audit_event_id FROM admin_audit_events
    WHERE occurred_at < $1
    ORDER BY occurred_at, audit_event_id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM admin_audit_events item USING selected
WHERE item.audit_event_id = selected.audit_event_id`, cutoff, limit)
			if err != nil {
				return 0, fmt.Errorf("delete admin audit retention batch: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return 0, fmt.Errorf("commit admin audit retention: %w", err)
			}
			return tag.RowsAffected(), nil
		},
	}}
}
