package observability

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type BacklogKey struct {
	Kind  string
	State string
}

type BacklogSample struct {
	BacklogKey
	Count     int64
	OldestAge time.Duration
}

type BacklogSource interface {
	BacklogSnapshot(context.Context) ([]BacklogSample, error)
}

type backlogCollector struct {
	source      BacklogSource
	expected    []BacklogKey
	timeout     time.Duration
	backlog     *prometheus.Desc
	oldestAge   *prometheus.Desc
	snapshotOK  *prometheus.Desc
	expectedSet map[BacklogKey]struct{}
}

func RegisterBacklogMetrics(registerer prometheus.Registerer, service string, source BacklogSource, expected []BacklogKey) error {
	if registerer == nil || service == "" || source == nil || len(expected) == 0 {
		return fmt.Errorf("backlog metrics registerer, service, source, and expected series are required")
	}
	expectedSet := make(map[BacklogKey]struct{}, len(expected))
	for _, key := range expected {
		if !validBacklogKey(key) {
			return fmt.Errorf("invalid backlog metric key %q/%q", key.Kind, key.State)
		}
		if _, exists := expectedSet[key]; exists {
			return fmt.Errorf("duplicate backlog metric key %q/%q", key.Kind, key.State)
		}
		expectedSet[key] = struct{}{}
	}
	collector := &backlogCollector{
		source:      source,
		expected:    append([]BacklogKey(nil), expected...),
		timeout:     time.Second,
		expectedSet: expectedSet,
		backlog: prometheus.NewDesc(
			"vpn_platform_message_backlog",
			"Current durable message backlog by bounded owner-local kind and state.",
			[]string{"kind", "state"}, nil,
		),
		oldestAge: prometheus.NewDesc(
			"vpn_platform_message_oldest_age_seconds",
			"Age of the oldest durable message by bounded owner-local kind and state.",
			[]string{"kind", "state"}, nil,
		),
		snapshotOK: prometheus.NewDesc(
			"vpn_platform_message_snapshot_success",
			"Whether the latest owner-local durable message snapshot succeeded.",
			nil, nil,
		),
	}
	return prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer).Register(collector)
}

func (c *backlogCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.backlog
	ch <- c.oldestAge
	ch <- c.snapshotOK
}

func (c *backlogCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	samples, err := c.source.BacklogSnapshot(ctx)
	values := make(map[BacklogKey]BacklogSample, len(samples))
	if err == nil {
		for _, sample := range samples {
			if _, ok := c.expectedSet[sample.BacklogKey]; !ok || sample.Count < 0 || sample.OldestAge < 0 || math.IsInf(sample.OldestAge.Seconds(), 0) || math.IsNaN(sample.OldestAge.Seconds()) {
				err = fmt.Errorf("invalid backlog sample")
				break
			}
			if _, exists := values[sample.BacklogKey]; exists {
				err = fmt.Errorf("duplicate backlog sample")
				break
			}
			values[sample.BacklogKey] = sample
		}
	}
	success := 1.0
	if err != nil {
		success = 0
		values = nil
	}
	ch <- prometheus.MustNewConstMetric(c.snapshotOK, prometheus.GaugeValue, success)
	for _, key := range c.expected {
		sample := values[key]
		ch <- prometheus.MustNewConstMetric(c.backlog, prometheus.GaugeValue, float64(sample.Count), key.Kind, key.State)
		ch <- prometheus.MustNewConstMetric(c.oldestAge, prometheus.GaugeValue, sample.OldestAge.Seconds(), key.Kind, key.State)
	}
}

func validBacklogKey(key BacklogKey) bool {
	switch key.Kind {
	case "outbox", "inbox", "dlq", "notification", "operation":
	default:
		return false
	}
	switch key.State {
	case "pending", "processing", "retry", "dead", "available", "replay_requested", "permanently_failed":
		return true
	default:
		return false
	}
}
