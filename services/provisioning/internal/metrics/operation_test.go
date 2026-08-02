package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func TestOperationMetricsBoundKind(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics, err := NewOperationMetrics(registry, "provisioning-service")
	if err != nil {
		t.Fatal(err)
	}
	metrics.ObserveOperation("credential-secret", time.Second)

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
	if serialized := output.String(); !strings.Contains(serialized, `kind="other"`) || strings.Contains(serialized, "credential-secret") {
		t.Fatalf("operation metric labels are not bounded:\n%s", serialized)
	}
}
