package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/observability"
	accessmetrics "github.com/ZheglY/vpn-platform/services/access/internal/metrics"
)

func (s *Store) PaymentAccessSnapshot(ctx context.Context) (accessmetrics.PaymentAccessSnapshot, error) {
	var snapshot accessmetrics.PaymentAccessSnapshot
	err := s.pool.QueryRow(ctx, `
SELECT count(*)::bigint,
       count(*) FILTER (
           WHERE fulfilled_at > paid_at + interval '60 seconds'
              OR (fulfilled_at IS NULL AND clock_timestamp() >= paid_at + interval '60 seconds')
       )::bigint
FROM payment_access_sli
WHERE payment_event_id IS NOT NULL`).Scan(&snapshot.Started, &snapshot.Bad)
	if err != nil {
		return accessmetrics.PaymentAccessSnapshot{}, fmt.Errorf("query durable payment access SLI: %w", err)
	}
	return snapshot, nil
}

func BacklogSeries() []observability.BacklogKey {
	return []observability.BacklogKey{
		{Kind: "outbox", State: "pending"},
		{Kind: "outbox", State: "processing"},
		{Kind: "dlq", State: "available"},
	}
}

func StateSeries() []observability.StateKey {
	series := make([]observability.StateKey, 0, 12)
	for _, state := range []string{"provisioning", "active", "degraded", "failed", "revoking", "revoked"} {
		series = append(series, observability.StateKey{Kind: "credential", State: state})
	}
	for _, kind := range []string{"access_operation_provision", "access_operation_revoke"} {
		for _, state := range []string{"pending", "succeeded", "failed"} {
			series = append(series, observability.StateKey{Kind: kind, State: state})
		}
	}
	return series
}

func (s *Store) StateSnapshot(ctx context.Context) ([]observability.StateSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint
FROM (
    SELECT 'credential'::text AS kind, status AS state FROM access_credentials
    UNION ALL
    SELECT 'access_operation_' || kind, status FROM access_operations
) AS domain_state
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query access state metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.StateSample
	for rows.Next() {
		var sample observability.StateSample
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count); err != nil {
			return nil, fmt.Errorf("scan access state metrics: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access state metrics: %w", err)
	}
	return samples, nil
}

func (s *Store) BacklogSnapshot(ctx context.Context) ([]observability.BacklogSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint,
       GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(created_at)), 0)::double precision
FROM (
    SELECT 'outbox'::text AS kind, state, created_at
    FROM outbox WHERE state IN ('pending', 'processing')
    UNION ALL
    SELECT 'dlq'::text, 'available'::text, first_seen_at
    FROM consumer_dead_letters
) AS backlog
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query access backlog metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.BacklogSample
	for rows.Next() {
		var sample observability.BacklogSample
		var oldestSeconds float64
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count, &oldestSeconds); err != nil {
			return nil, fmt.Errorf("scan access backlog metrics: %w", err)
		}
		sample.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access backlog metrics: %w", err)
	}
	return samples, nil
}
