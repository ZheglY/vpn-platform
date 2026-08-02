package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type paymentAccessSourceFunc func(context.Context) (PaymentAccessSnapshot, error)

func (f paymentAccessSourceFunc) PaymentAccessSnapshot(ctx context.Context) (PaymentAccessSnapshot, error) {
	return f(ctx)
}

func TestPaymentAccessCollectorExportsDurableCounters(t *testing.T) {
	registry := prometheus.NewRegistry()
	if err := RegisterPaymentAccessSLI(registry, paymentAccessSourceFunc(func(context.Context) (PaymentAccessSnapshot, error) {
		return PaymentAccessSnapshot{Started: 7, Bad: 2}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if got := gatheredMetric(t, registry, "vpn_access_payment_access_fulfillment_started_total"); got != 7 {
		t.Fatalf("started = %v, want 7", got)
	}
	if got := gatheredMetric(t, registry, "vpn_access_payment_access_fulfillment_bad_total"); got != 2 {
		t.Fatalf("bad = %v, want 2", got)
	}
}

func TestPaymentAccessCollectorFailsClosed(t *testing.T) {
	registry := prometheus.NewRegistry()
	if err := RegisterPaymentAccessSLI(registry, paymentAccessSourceFunc(func(context.Context) (PaymentAccessSnapshot, error) {
		return PaymentAccessSnapshot{}, errors.New("unavailable")
	})); err != nil {
		t.Fatal(err)
	}
	if got := gatheredMetric(t, registry, "vpn_access_payment_access_fulfillment_snapshot_success"); got != 0 {
		t.Fatalf("snapshot success = %v, want 0", got)
	}
}

func gatheredMetric(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name || len(family.Metric) != 1 {
			continue
		}
		if family.Metric[0].Counter != nil {
			return family.Metric[0].Counter.GetValue()
		}
		if family.Metric[0].Gauge != nil {
			return family.Metric[0].Gauge.GetValue()
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}
