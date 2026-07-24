package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type backlogSourceFunc func(context.Context) ([]BacklogSample, error)

func (f backlogSourceFunc) BacklogSnapshot(ctx context.Context) ([]BacklogSample, error) {
	return f(ctx)
}

func TestBacklogCollectorExportsExpectedZeroAndSnapshotState(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	source := backlogSourceFunc(func(context.Context) ([]BacklogSample, error) {
		return []BacklogSample{{
			BacklogKey: BacklogKey{Kind: "outbox", State: "pending"},
			Count:      3,
			OldestAge:  2 * time.Minute,
		}}, nil
	})
	expected := []BacklogKey{{Kind: "outbox", State: "pending"}, {Kind: "outbox", State: "processing"}}
	if err := RegisterBacklogMetrics(registry, "billing-service", source, expected); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, registry, "vpn_platform_message_backlog", map[string]string{"service": "billing-service", "kind": "outbox", "state": "pending"}); got != 3 {
		t.Fatalf("pending backlog = %v, want 3", got)
	}
	if got := gaugeValue(t, registry, "vpn_platform_message_backlog", map[string]string{"service": "billing-service", "kind": "outbox", "state": "processing"}); got != 0 {
		t.Fatalf("processing backlog = %v, want 0", got)
	}
	if got := gaugeValue(t, registry, "vpn_platform_message_snapshot_success", map[string]string{"service": "billing-service"}); got != 1 {
		t.Fatalf("snapshot success = %v, want 1", got)
	}
}

func TestBacklogCollectorFailsClosedForUnexpectedLabel(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	source := backlogSourceFunc(func(context.Context) ([]BacklogSample, error) {
		return []BacklogSample{{
			BacklogKey: BacklogKey{Kind: "user-secret", State: "payment-secret"},
			Count:      1,
		}}, nil
	})
	if err := RegisterBacklogMetrics(registry, "billing-service", source, []BacklogKey{{Kind: "outbox", State: "pending"}}); err != nil {
		t.Fatal(err)
	}
	serialized := gatherText(t, registry)
	if got := gaugeValue(t, registry, "vpn_platform_message_snapshot_success", map[string]string{"service": "billing-service"}); got != 0 {
		t.Fatalf("snapshot success = %v, want 0", got)
	}
	if strings.Contains(serialized, "user-secret") || strings.Contains(serialized, "payment-secret") {
		t.Fatalf("unexpected labels leaked:\n%s", serialized)
	}
}

func TestBacklogCollectorReportsSourceFailure(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	source := backlogSourceFunc(func(context.Context) ([]BacklogSample, error) {
		return nil, errors.New("database unavailable")
	})
	if err := RegisterBacklogMetrics(registry, "service", source, []BacklogKey{{Kind: "inbox", State: "pending"}}); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, registry, "vpn_platform_message_snapshot_success", map[string]string{"service": "service"}); got != 0 {
		t.Fatalf("snapshot success = %v, want 0", got)
	}
}

func gaugeValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			matched := len(metric.GetLabel()) == len(labels)
			for _, label := range metric.GetLabel() {
				if labels[label.GetName()] != label.GetValue() {
					matched = false
				}
			}
			if matched {
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s with labels %v was not found", name, labels)
	return 0
}
