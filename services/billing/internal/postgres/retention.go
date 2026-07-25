package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
)

func (s *Store) RetentionDatasets(keepFor time.Duration) []retention.Dataset {
	return []retention.Dataset{{
		Name:    "processed_webhooks",
		KeepFor: keepFor,
		Preview: func(ctx context.Context, cutoff time.Time) (retention.Eligibility, error) {
			var result retention.Eligibility
			err := s.pool.QueryRow(ctx, `
WITH candidates AS (
    SELECT count(*) AS count
    FROM webhook_inbox
    WHERE state = 'processed' AND processed_at < $1
), hold AS (
    SELECT EXISTS (
        SELECT 1 FROM retention_legal_holds
        WHERE scope IN ('all_financial_records', 'webhook_inbox')
          AND (active_until IS NULL OR active_until > clock_timestamp())
    ) AS active
)
SELECT CASE WHEN hold.active THEN 0 ELSE candidates.count END,
       CASE WHEN hold.active THEN candidates.count ELSE 0 END
FROM candidates CROSS JOIN hold`, cutoff).Scan(&result.Eligible, &result.Protected)
			if err != nil {
				return retention.Eligibility{}, fmt.Errorf("preview processed webhook retention: %w", err)
			}
			return result, nil
		},
		DeleteBatch: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
			tag, err := s.pool.Exec(ctx, `
WITH selected AS (
    SELECT id
    FROM webhook_inbox
    WHERE state = 'processed' AND processed_at < $1
      AND NOT EXISTS (
          SELECT 1 FROM retention_legal_holds
          WHERE scope IN ('all_financial_records', 'webhook_inbox')
            AND (active_until IS NULL OR active_until > clock_timestamp())
      )
    ORDER BY processed_at, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM webhook_inbox item USING selected
WHERE item.id = selected.id`, cutoff, limit)
			if err != nil {
				return 0, fmt.Errorf("delete processed webhook retention batch: %w", err)
			}
			return tag.RowsAffected(), nil
		},
	}}
}
