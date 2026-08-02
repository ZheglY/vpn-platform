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
		{Kind: "dlq", State: "available"},
		{Kind: "dlq", State: "replay_requested"},
		{Kind: "operation", State: "pending"},
		{Kind: "operation", State: "processing"},
		{Kind: "operation", State: "retry"},
	}
}

func StateSeries() []observability.StateKey {
	series := make([]observability.StateKey, 0, 23)
	for _, kind := range []string{"provisioning_operation_provision", "provisioning_operation_revoke"} {
		for _, state := range []string{"pending", "processing", "retry", "succeeded", "failed", "superseded"} {
			series = append(series, observability.StateKey{Kind: kind, State: state})
		}
	}
	for _, state := range []string{"active", "draining", "offline"} {
		series = append(series, observability.StateKey{Kind: "node", State: state})
	}
	for _, kind := range []string{"allocation_primary", "allocation_failover"} {
		for _, state := range []string{"pending", "active", "failed", "revoked"} {
			series = append(series, observability.StateKey{Kind: kind, State: state})
		}
	}
	return series
}

func (s *Store) StateSnapshot(ctx context.Context) ([]observability.StateSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint
FROM (
    SELECT 'provisioning_operation_' || kind AS kind, state FROM operations
    UNION ALL
    SELECT 'node'::text, status FROM nodes
    UNION ALL
    SELECT 'allocation_' || role, state FROM allocations
) AS domain_state
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query provisioning state metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.StateSample
	for rows.Next() {
		var sample observability.StateSample
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count); err != nil {
			return nil, fmt.Errorf("scan provisioning state metrics: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provisioning state metrics: %w", err)
	}
	return samples, nil
}

func (s *Store) RegisterProvisioningMetrics(registerer prometheus.Registerer, service string) error {
	collector := newProvisioningCollector(s, service)
	return registerer.Register(collector)
}

type provisioningCollector struct {
	store       *Store
	capacity    *prometheus.Desc
	utilization *prometheus.Desc
	heartbeat   *prometheus.Desc
	revision    *prometheus.Desc
	success     *prometheus.Desc
}

func newProvisioningCollector(store *Store, service string) *provisioningCollector {
	labels := prometheus.Labels{"service": service}
	return &provisioningCollector{
		store: store,
		capacity: prometheus.NewDesc(
			"vpn_provisioning_node_capacity_clients",
			"Provisioning node client capacity by bounded node status and capacity type.",
			[]string{"status", "type"}, labels,
		),
		utilization: prometheus.NewDesc(
			"vpn_provisioning_node_capacity_utilization_ratio",
			"Maximum allocated-to-limit client ratio by bounded node status.",
			[]string{"status"}, labels,
		),
		heartbeat: prometheus.NewDesc(
			"vpn_provisioning_node_heartbeat_age_seconds",
			"Maximum age of the latest node heartbeat by bounded node status.",
			[]string{"status"}, labels,
		),
		revision: prometheus.NewDesc(
			"vpn_provisioning_node_config_revision",
			"Maximum observed Xray configuration revision by bounded node status.",
			[]string{"status"}, labels,
		),
		success: prometheus.NewDesc(
			"vpn_provisioning_metrics_snapshot_success",
			"Whether the latest provisioning capacity snapshot succeeded.",
			nil, labels,
		),
	}
}

func (c *provisioningCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.capacity
	ch <- c.utilization
	ch <- c.heartbeat
	ch <- c.revision
	ch <- c.success
}

type capacitySample struct {
	limit       int64
	allocated   int64
	utilization float64
	heartbeat   float64
	revision    float64
}

func (c *provisioningCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := c.store.pool.Query(ctx, `
SELECT status,
       COALESCE(sum(capacity_limit), 0)::bigint,
       COALESCE(sum(allocated_clients), 0)::bigint,
       COALESCE(max(allocated_clients::double precision / capacity_limit), 0)::double precision,
       COALESCE(max(
           CASE WHEN last_seen_at IS NULL THEN 2592000
                ELSE GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - last_seen_at), 0)
           END
       ), 0)::double precision,
       COALESCE(max(config_revision), 0)::double precision
FROM nodes
GROUP BY status`)
	values := make(map[string]capacitySample, 3)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var status string
			var sample capacitySample
			if scanErr := rows.Scan(&status, &sample.limit, &sample.allocated, &sample.utilization, &sample.heartbeat, &sample.revision); scanErr != nil {
				err = scanErr
				break
			}
			switch status {
			case "active", "draining", "offline":
				values[status] = sample
			default:
				err = fmt.Errorf("unexpected node status")
			}
		}
		if rowsErr := rows.Err(); err == nil && rowsErr != nil {
			err = rowsErr
		}
	}
	success := 1.0
	if err != nil {
		success = 0
		values = nil
	}
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, success)
	for _, status := range []string{"active", "draining", "offline"} {
		sample := values[status]
		ch <- prometheus.MustNewConstMetric(c.capacity, prometheus.GaugeValue, float64(sample.limit), status, "limit")
		ch <- prometheus.MustNewConstMetric(c.capacity, prometheus.GaugeValue, float64(sample.allocated), status, "allocated")
		ch <- prometheus.MustNewConstMetric(c.utilization, prometheus.GaugeValue, sample.utilization, status)
		ch <- prometheus.MustNewConstMetric(c.heartbeat, prometheus.GaugeValue, sample.heartbeat, status)
		ch <- prometheus.MustNewConstMetric(c.revision, prometheus.GaugeValue, sample.revision, status)
	}
}

func (s *Store) BacklogSnapshot(ctx context.Context) ([]observability.BacklogSample, error) {
	rows, err := s.pool.Query(ctx, `
SELECT kind, state, count(*)::bigint,
       GREATEST(EXTRACT(EPOCH FROM clock_timestamp() - min(created_at)), 0)::double precision
FROM (
    SELECT 'outbox'::text AS kind, state, created_at
    FROM outbox WHERE state IN ('pending', 'processing')
    UNION ALL
    SELECT 'dlq'::text,
           CASE replay_state WHEN 'requested' THEN 'replay_requested' ELSE replay_state END,
           first_seen_at
    FROM consumer_dead_letters WHERE replay_state IN ('available', 'requested')
    UNION ALL
    SELECT 'operation'::text, state, created_at
    FROM operations WHERE state IN ('pending', 'processing', 'retry')
) AS backlog
GROUP BY kind, state`)
	if err != nil {
		return nil, fmt.Errorf("query provisioning backlog metrics: %w", err)
	}
	defer rows.Close()
	var samples []observability.BacklogSample
	for rows.Next() {
		var sample observability.BacklogSample
		var oldestSeconds float64
		if err := rows.Scan(&sample.Kind, &sample.State, &sample.Count, &oldestSeconds); err != nil {
			return nil, fmt.Errorf("scan provisioning backlog metrics: %w", err)
		}
		sample.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provisioning backlog metrics: %w", err)
	}
	return samples, nil
}
