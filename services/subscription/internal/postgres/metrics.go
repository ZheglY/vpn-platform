package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ZheglY/vpn-platform/internal/platform/observability"
)

func BacklogSeries() []observability.BacklogKey {
	return []observability.BacklogKey{
		{Kind: "outbox", State: "pending"},
		{Kind: "outbox", State: "processing"},
		{Kind: "inbox", State: "pending"},
		{Kind: "inbox", State: "processing"},
		{Kind: "inbox", State: "dead"},
	}
}

func StateSeries() []observability.StateKey {
	states := []string{"pending", "active", "grace", "expired", "suspended", "revoked"}
	series := make([]observability.StateKey, 0, len(states))
	for _, state := range states {
		series = append(series, observability.StateKey{Kind: "subscription", State: state})
	}
	return series
}

func (s *Store) StateSnapshot(ctx context.Context) ([]observability.StateSample, error) {
	rows, err := s.pool.Query(ctx, `SELECT status, count(*)::bigint FROM subscriptions GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("query subscription state metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.StateSample
	for rows.Next() {
		sample := observability.StateSample{StateKey: observability.StateKey{Kind: "subscription"}}
		if err := rows.Scan(&sample.State, &sample.Count); err != nil {
			return nil, fmt.Errorf("scan subscription state metrics: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription state metrics: %w", err)
	}
	return samples, nil
}

func (s *Store) RegisterSubscriptionMetrics(registerer prometheus.Registerer, service string) error {
	collector := &subscriptionCollector{
		store: s,
		due: prometheus.NewDesc(
			"vpn_subscription_scheduler_due",
			"Current subscriptions whose lifecycle transition is due.",
			nil, prometheus.Labels{"service": service},
		),
		lag: prometheus.NewDesc(
			"vpn_subscription_scheduler_lag_seconds",
			"Age of the oldest due subscription lifecycle transition.",
			nil, prometheus.Labels{"service": service},
		),
		success: prometheus.NewDesc(
			"vpn_subscription_metrics_snapshot_success",
			"Whether the latest subscription metrics snapshot succeeded.",
			nil, prometheus.Labels{"service": service},
		),
	}
	return registerer.Register(collector)
}

type subscriptionCollector struct {
	store   *Store
	due     *prometheus.Desc
	lag     *prometheus.Desc
	success *prometheus.Desc
}

func (c *subscriptionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.due
	ch <- c.lag
	ch <- c.success
}

func (c *subscriptionCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var due int64
	var lag float64
	err := c.store.pool.QueryRow(ctx, `
SELECT count(*)::bigint,
       COALESCE(GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(next_transition_at)), 0), 0)::double precision
FROM subscriptions
WHERE status IN ('pending', 'active', 'grace')
  AND next_transition_at <= clock_timestamp()`).Scan(&due, &lag)
	success := 1.0
	if err != nil {
		success, due, lag = 0, 0, 0
	}
	ch <- prometheus.MustNewConstMetric(c.due, prometheus.GaugeValue, float64(due))
	ch <- prometheus.MustNewConstMetric(c.lag, prometheus.GaugeValue, lag)
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, success)
}

func (s *Store) BacklogSnapshot(ctx context.Context) ([]observability.BacklogSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint,
       GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(created_at)), 0)::double precision
FROM (
    SELECT 'outbox'::text AS kind, state, created_at
    FROM outbox WHERE state IN ('pending', 'processing')
    UNION ALL
    SELECT 'inbox'::text, state, received_at
    FROM inbox WHERE state IN ('pending', 'processing', 'dead')
) AS backlog
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query subscription backlog metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.BacklogSample
	for rows.Next() {
		var sample observability.BacklogSample
		var oldestSeconds float64
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count, &oldestSeconds); err != nil {
			return nil, fmt.Errorf("scan subscription backlog metrics: %w", err)
		}
		sample.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription backlog metrics: %w", err)
	}
	return samples, nil
}
