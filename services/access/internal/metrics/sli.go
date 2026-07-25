package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const maxPaymentProvisioningLatency = 30 * 24 * time.Hour

type PaymentProvisioning struct {
	latency prometheus.Histogram
}

func NewPaymentProvisioning(registerer prometheus.Registerer) *PaymentProvisioning {
	metric := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "vpn_access",
		Name:      "payment_to_provisioning_seconds",
		Help:      "Elapsed time from an initial successful payment period start to the durable provisioning command.",
		Buckets:   []float64{1, 2, 5, 10, 20, 30, 45, 60, 90, 120, 300, 900},
	})
	registerer.MustRegister(metric)
	return &PaymentProvisioning{latency: metric}
}

func (m *PaymentProvisioning) ObservePaymentToProvisioning(elapsed time.Duration) {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > maxPaymentProvisioningLatency {
		elapsed = maxPaymentProvisioningLatency
	}
	m.latency.Observe(elapsed.Seconds())
}
