package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type OperationMetrics struct {
	duration *prometheus.HistogramVec
}

func NewOperationMetrics(registerer prometheus.Registerer, service string) (*OperationMetrics, error) {
	if registerer == nil || service == "" {
		return nil, fmt.Errorf("operation metrics registerer and service are required")
	}
	registerer = prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer)
	metrics := &OperationMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "vpn_provisioning",
			Subsystem: "operation",
			Name:      "duration_seconds",
			Help:      "Provisioning operation attempt duration by bounded operation kind.",
			Buckets:   []float64{0.01, 0.03, 0.1, 0.3, 1, 3, 10, 30},
		}, []string{"kind"}),
	}
	registerer.MustRegister(metrics.duration)
	return metrics, nil
}

func (m *OperationMetrics) ObserveOperation(kind string, duration time.Duration) {
	switch kind {
	case "provision", "revoke":
	default:
		kind = "other"
	}
	m.duration.WithLabelValues(kind).Observe(duration.Seconds())
}
