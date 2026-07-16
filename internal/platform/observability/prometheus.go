package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	return registry
}

func Handler(registry *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
