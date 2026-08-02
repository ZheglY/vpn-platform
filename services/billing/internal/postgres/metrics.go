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
	states := []string{"created", "verification_pending", "pending", "succeeded", "canceled", "failed"}
	series := make([]observability.StateKey, 0, len(states))
	for _, state := range states {
		series = append(series, observability.StateKey{Kind: "payment", State: state})
	}
	return series
}

func (s *Store) StateSnapshot(ctx context.Context) ([]observability.StateSample, error) {
	rows, err := s.pool.Query(ctx, `SELECT status, count(*)::bigint FROM payments GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("query billing state metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.StateSample
	for rows.Next() {
		sample := observability.StateSample{StateKey: observability.StateKey{Kind: "payment"}}
		if err := rows.Scan(&sample.State, &sample.Count); err != nil {
			return nil, fmt.Errorf("scan billing state metrics: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing state metrics: %w", err)
	}
	return samples, nil
}

func (s *Store) RegisterBillingMetrics(registerer prometheus.Registerer, service string) error {
	collector := &billingCollector{
		store: s,
		due: prometheus.NewDesc(
			"vpn_billing_reconciliation_due",
			"Current payments whose provider reconciliation is due.",
			nil, prometheus.Labels{"service": service},
		),
		lag: prometheus.NewDesc(
			"vpn_billing_reconciliation_lag_seconds",
			"Age of the oldest due payment reconciliation.",
			nil, prometheus.Labels{"service": service},
		),
		success: prometheus.NewDesc(
			"vpn_billing_metrics_snapshot_success",
			"Whether the latest billing metrics snapshot succeeded.",
			nil, prometheus.Labels{"service": service},
		),
	}
	return registerer.Register(collector)
}

type billingCollector struct {
	store   *Store
	due     *prometheus.Desc
	lag     *prometheus.Desc
	success *prometheus.Desc
}

func (c *billingCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.due
	ch <- c.lag
	ch <- c.success
}

func (c *billingCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var due int64
	var lag float64
	err := c.store.pool.QueryRow(ctx, `
SELECT count(*)::bigint,
       COALESCE(GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(next_reconcile_at)), 0), 0)::double precision
FROM payments
WHERE status IN ('created', 'verification_pending', 'pending')
  AND next_reconcile_at <= clock_timestamp()`).Scan(&due, &lag)
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
    FROM webhook_inbox WHERE state IN ('pending', 'processing', 'dead')
) AS backlog
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query billing backlog metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.BacklogSample
	for rows.Next() {
		var sample observability.BacklogSample
		var oldestSeconds float64
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count, &oldestSeconds); err != nil {
			return nil, fmt.Errorf("scan billing backlog metrics: %w", err)
		}
		sample.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing backlog metrics: %w", err)
	}
	return samples, nil
}
