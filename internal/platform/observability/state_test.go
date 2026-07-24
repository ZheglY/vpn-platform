package observability

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type stateSourceFunc func(context.Context) ([]StateSample, error)

func (f stateSourceFunc) StateSnapshot(ctx context.Context) ([]StateSample, error) {
	return f(ctx)
}

func TestStateCollectorBoundsLabelsAndEmitsZeros(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	source := stateSourceFunc(func(context.Context) ([]StateSample, error) {
		return []StateSample{{StateKey: StateKey{Kind: "payment", State: "succeeded"}, Count: 4}}, nil
	})
	expected := []StateKey{{Kind: "payment", State: "pending"}, {Kind: "payment", State: "succeeded"}}
	if err := RegisterStateMetrics(registry, "billing-service", source, expected); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, registry, "vpn_platform_domain_objects", map[string]string{"service": "billing-service", "kind": "payment", "state": "pending"}); got != 0 {
		t.Fatalf("pending payments = %v, want 0", got)
	}
	if got := gaugeValue(t, registry, "vpn_platform_domain_objects", map[string]string{"service": "billing-service", "kind": "payment", "state": "succeeded"}); got != 4 {
		t.Fatalf("succeeded payments = %v, want 4", got)
	}
}

func TestStateCollectorFailsClosedForUnexpectedState(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	source := stateSourceFunc(func(context.Context) ([]StateSample, error) {
		return []StateSample{{StateKey: StateKey{Kind: "payment", State: "user_secret"}, Count: 1}}, nil
	})
	if err := RegisterStateMetrics(registry, "billing-service", source, []StateKey{{Kind: "payment", State: "pending"}}); err != nil {
		t.Fatal(err)
	}
	serialized := gatherText(t, registry)
	if strings.Contains(serialized, "user_secret") {
		t.Fatalf("unexpected state leaked:\n%s", serialized)
	}
	if got := gaugeValue(t, registry, "vpn_platform_domain_snapshot_success", map[string]string{"service": "billing-service"}); got != 0 {
		t.Fatalf("snapshot success = %v, want 0", got)
	}
}
