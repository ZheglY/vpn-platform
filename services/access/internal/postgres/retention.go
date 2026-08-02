package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
)

func (s *Store) RetentionClock(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read access retention clock: %w", err)
	}
	return now.UTC(), nil
}

func (s *Store) RetentionDatasets(keepFor time.Duration) []retention.Dataset {
	return []retention.Dataset{{
		Name:    "security_audit_events",
		KeepFor: keepFor,
		Preview: func(ctx context.Context, cutoff time.Time) (retention.Eligibility, error) {
			var count int64
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_audit_events WHERE occurred_at < $1`, cutoff).Scan(&count); err != nil {
				return retention.Eligibility{}, fmt.Errorf("preview security audit retention: %w", err)
			}
			return retention.Eligibility{Eligible: count}, nil
		},
		DeleteBatch: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
			tag, err := s.pool.Exec(ctx, `
WITH selected AS (
    SELECT event_id FROM security_audit_events
    WHERE occurred_at < $1
    ORDER BY occurred_at, event_id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM security_audit_events item USING selected
WHERE item.event_id = selected.event_id`, cutoff, limit)
			if err != nil {
				return 0, fmt.Errorf("delete security audit retention batch: %w", err)
			}
			return tag.RowsAffected(), nil
		},
	}}
}
