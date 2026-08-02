package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/observability"
)

func BacklogSeries() []observability.BacklogKey {
	return []observability.BacklogKey{
		{Kind: "notification", State: "pending"},
		{Kind: "notification", State: "processing"},
		{Kind: "notification", State: "retry"},
		{Kind: "notification", State: "permanently_failed"},
		{Kind: "dlq", State: "available"},
		{Kind: "dlq", State: "replay_requested"},
	}
}

func (s *Store) BacklogSnapshot(ctx context.Context) ([]observability.BacklogSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint,
       GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(created_at)), 0)::double precision
FROM (
    SELECT 'notification'::text AS kind, status AS state, created_at
    FROM notification_jobs
    WHERE status IN ('pending', 'processing', 'retry', 'permanently_failed')
    UNION ALL
    SELECT 'dlq'::text, state, created_at
    FROM notification_dead_letters WHERE state IN ('available', 'replay_requested')
) AS backlog
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query notification backlog metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.BacklogSample
	for rows.Next() {
		var sample observability.BacklogSample
		var oldestSeconds float64
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count, &oldestSeconds); err != nil {
			return nil, fmt.Errorf("scan notification backlog metrics: %w", err)
		}
		sample.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification backlog metrics: %w", err)
	}
	return samples, nil
}
