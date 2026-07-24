package observability

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var stateLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

type StateKey struct {
	Kind  string
	State string
}

type StateSample struct {
	StateKey
	Count int64
}

type StateSource interface {
	StateSnapshot(context.Context) ([]StateSample, error)
}

type stateCollector struct {
	source      StateSource
	expected    []StateKey
	expectedSet map[StateKey]struct{}
	objects     *prometheus.Desc
	snapshotOK  *prometheus.Desc
}

func RegisterStateMetrics(registerer prometheus.Registerer, service string, source StateSource, expected []StateKey) error {
	if registerer == nil || service == "" || source == nil || len(expected) == 0 {
		return fmt.Errorf("state metrics registerer, service, source, and expected series are required")
	}
	expectedSet := make(map[StateKey]struct{}, len(expected))
	for _, key := range expected {
		if !stateLabelPattern.MatchString(key.Kind) || !stateLabelPattern.MatchString(key.State) {
			return fmt.Errorf("invalid state metric key %q/%q", key.Kind, key.State)
		}
		if _, exists := expectedSet[key]; exists {
			return fmt.Errorf("duplicate state metric key %q/%q", key.Kind, key.State)
		}
		expectedSet[key] = struct{}{}
	}
	collector := &stateCollector{
		source:      source,
		expected:    append([]StateKey(nil), expected...),
		expectedSet: expectedSet,
		objects: prometheus.NewDesc(
			"vpn_platform_domain_objects",
			"Current owner-local domain objects by explicitly allowlisted kind and state.",
			[]string{"kind", "state"}, nil,
		),
		snapshotOK: prometheus.NewDesc(
			"vpn_platform_domain_snapshot_success",
			"Whether the latest owner-local domain state snapshot succeeded.",
			nil, nil,
		),
	}
	return prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer).Register(collector)
}

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.objects
	ch <- c.snapshotOK
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	samples, err := c.source.StateSnapshot(ctx)
	values := make(map[StateKey]int64, len(samples))
	if err == nil {
		for _, sample := range samples {
			if _, ok := c.expectedSet[sample.StateKey]; !ok || sample.Count < 0 {
				err = fmt.Errorf("invalid state sample")
				break
			}
			if _, exists := values[sample.StateKey]; exists {
				err = fmt.Errorf("duplicate state sample")
				break
			}
			values[sample.StateKey] = sample.Count
		}
	}
	success := 1.0
	if err != nil {
		success = 0
		values = nil
	}
	ch <- prometheus.MustNewConstMetric(c.snapshotOK, prometheus.GaugeValue, success)
	for _, key := range c.expected {
		ch <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(values[key]), key.Kind, key.State)
	}
}
