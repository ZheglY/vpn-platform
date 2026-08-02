package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

type Metrics struct {
	activeClients  prometheus.Gauge
	configRevision prometheus.Gauge
	xrayHealthy    prometheus.Gauge
	reloads        *prometheus.CounterVec
	reloadDuration *prometheus.HistogramVec
	lastFailure    prometheus.Gauge
}

func New(registerer prometheus.Registerer, service string) (*Metrics, error) {
	if registerer == nil || service == "" {
		return nil, fmt.Errorf("node metrics registerer and service are required")
	}
	registerer = prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer)
	metrics := &Metrics{
		activeClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "vpn_node",
			Name:      "active_clients",
			Help:      "Current active VLESS clients on this node-agent instance.",
		}),
		configRevision: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "vpn_node",
			Name:      "config_revision",
			Help:      "Current installed Xray configuration revision on this node-agent instance.",
		}),
		xrayHealthy: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "vpn_node",
			Name:      "xray_healthy",
			Help:      "Whether the supervised Xray process is currently healthy.",
		}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "vpn_node",
			Subsystem: "xray",
			Name:      "reloads_total",
			Help:      "Xray reload attempts by bounded outcome.",
		}, []string{"outcome"}),
		reloadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "vpn_node",
			Subsystem: "xray",
			Name:      "reload_duration_seconds",
			Help:      "Xray reload duration by bounded outcome.",
			Buckets:   []float64{0.01, 0.03, 0.1, 0.3, 1, 3, 10, 30},
		}, []string{"outcome"}),
		lastFailure: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "vpn_node",
			Subsystem: "xray",
			Name:      "last_reload_failure_timestamp_seconds",
			Help:      "Unix timestamp of the latest failed Xray reload attempt.",
		}),
	}
	registerer.MustRegister(metrics.activeClients, metrics.configRevision, metrics.xrayHealthy, metrics.reloads, metrics.reloadDuration, metrics.lastFailure)
	return metrics, nil
}

func (m *Metrics) UpdateStatus(status domain.Status) {
	m.activeClients.Set(float64(status.ActiveClients))
	m.configRevision.Set(float64(status.ConfigRevision))
	if status.XrayHealthy {
		m.xrayHealthy.Set(1)
	} else {
		m.xrayHealthy.Set(0)
	}
}

func (m *Metrics) ObserveReload(outcome string, duration time.Duration) {
	switch outcome {
	case "success", "validation_error", "rollback_restored", "rollback_failed":
	default:
		outcome = "error"
	}
	m.reloads.WithLabelValues(outcome).Inc()
	m.reloadDuration.WithLabelValues(outcome).Observe(duration.Seconds())
	if outcome != "success" {
		m.lastFailure.SetToCurrentTime()
	}
}
