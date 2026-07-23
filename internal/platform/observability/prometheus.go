package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
)

func NewRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return registry
}

func MTLSHandler(registry *prometheus.Registry, trustDomain, namespace string) http.Handler {
	requireObservability := httpauth.RequireService(httpauth.ServicePolicy{
		TrustDomain: trustDomain,
		Namespace:   namespace,
		Allowed:     []string{"observability"},
	})
	return requireObservability(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}
