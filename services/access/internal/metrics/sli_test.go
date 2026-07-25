package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func TestPaymentProvisioningBoundsElapsedTime(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metric := NewPaymentProvisioning(registry)
	metric.ObservePaymentToProvisioning(-time.Second)
	metric.ObservePaymentToProvisioning(60 * 24 * time.Hour)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	encoder := expfmt.NewEncoder(&output, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}
	serialized := output.String()
	for _, expected := range []string{
		"vpn_access_payment_to_provisioning_seconds_count 2",
		`vpn_access_payment_to_provisioning_seconds_bucket{le="1"}`,
		`vpn_access_payment_to_provisioning_seconds_bucket{le="900"}`,
	} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("metric output is missing %q:\n%s", expected, serialized)
		}
	}
}
