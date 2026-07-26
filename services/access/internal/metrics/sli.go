package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type PaymentAccessSnapshot struct {
	Started int64
	Bad     int64
}

type PaymentAccessSource interface {
	PaymentAccessSnapshot(context.Context) (PaymentAccessSnapshot, error)
}

type paymentAccessCollector struct {
	source     PaymentAccessSource
	started    *prometheus.Desc
	bad        *prometheus.Desc
	snapshotOK *prometheus.Desc
}

func RegisterPaymentAccessSLI(registerer prometheus.Registerer, source PaymentAccessSource) error {
	if registerer == nil || source == nil {
		return fmt.Errorf("payment access SLI registerer and source are required")
	}
	return registerer.Register(&paymentAccessCollector{
		source: source,
		started: prometheus.NewDesc(
			"vpn_access_payment_access_fulfillment_started_total",
			"Durable successful-payment workflow starts observed from the billing event stream.",
			nil, prometheus.Labels{"service": "access-service"},
		),
		bad: prometheus.NewDesc(
			"vpn_access_payment_access_fulfillment_bad_total",
			"Durable payment workflows fulfilled after sixty seconds or still unfinished after the deadline.",
			nil, prometheus.Labels{"service": "access-service"},
		),
		snapshotOK: prometheus.NewDesc(
			"vpn_access_payment_access_fulfillment_snapshot_success",
			"Whether the latest durable payment workflow snapshot succeeded.",
			nil, prometheus.Labels{"service": "access-service"},
		),
	})
}

func (c *paymentAccessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.started
	ch <- c.bad
	ch <- c.snapshotOK
}

func (c *paymentAccessCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := c.source.PaymentAccessSnapshot(ctx)
	success := 1.0
	if err != nil || snapshot.Started < 0 || snapshot.Bad < 0 || snapshot.Bad > snapshot.Started {
		success = 0
		snapshot = PaymentAccessSnapshot{}
	}
	ch <- prometheus.MustNewConstMetric(c.started, prometheus.CounterValue, float64(snapshot.Started))
	ch <- prometheus.MustNewConstMetric(c.bad, prometheus.CounterValue, float64(snapshot.Bad))
	ch <- prometheus.MustNewConstMetric(c.snapshotOK, prometheus.GaugeValue, success)
}
