package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

func TestMetricsExposeOnlyBoundedRuntimeState(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics, err := New(registry, "node-agent")
	if err != nil {
		t.Fatal(err)
	}
	metrics.UpdateStatus(domain.Status{NodeID: "node-secret", ConfigRevision: 7, ActiveClients: 3, XrayHealthy: true})
	metrics.ObserveReload("credential-secret", time.Second)

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
	for _, expected := range []string{"vpn_node_active_clients", "vpn_node_config_revision", "vpn_node_xray_healthy", "vpn_node_xray_last_reload_failure_timestamp_seconds", `outcome="error"`} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("metric output is missing %q:\n%s", expected, serialized)
		}
	}
	for _, forbidden := range []string{"node-secret", "credential-secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("metric output leaked %q:\n%s", forbidden, serialized)
		}
	}
}
