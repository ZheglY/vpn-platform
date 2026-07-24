package kafka

import (
	"fmt"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kgo"
)

const maxObservedRecordAge = 30 * 24 * time.Hour

var topicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,248}$`)

type Observer interface {
	ObserveHandler(topic, outcome string, startedAt, recordTimestamp time.Time)
	ObserveOutbox(topic, outcome string, startedAt time.Time)
	ObserveRetry(topic, stage string)
	ObserveDLQ(topic string)
}

type NoopObserver struct{}

func (NoopObserver) ObserveHandler(string, string, time.Time, time.Time) {}
func (NoopObserver) ObserveOutbox(string, string, time.Time)             {}
func (NoopObserver) ObserveRetry(string, string)                         {}
func (NoopObserver) ObserveDLQ(string)                                   {}

func ObserverOrNoop(observers ...Observer) Observer {
	if len(observers) > 0 && observers[0] != nil {
		return observers[0]
	}
	return NoopObserver{}
}

type Metrics struct {
	topics          map[string]struct{}
	records         *prometheus.CounterVec
	handlerDuration *prometheus.HistogramVec
	consumerLag     *prometheus.GaugeVec
	lastObserved    *prometheus.GaugeVec
	retries         *prometheus.CounterVec
	deadLetters     *prometheus.CounterVec
}

func NewMetrics(registerer prometheus.Registerer, service string, topics []string) (*Metrics, error) {
	if registerer == nil || service == "" || len(topics) == 0 {
		return nil, fmt.Errorf("kafka metrics registerer, service, and topics are required")
	}
	allowed := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		if !topicPattern.MatchString(topic) {
			return nil, fmt.Errorf("kafka metric topic %q is invalid", topic)
		}
		if _, exists := allowed[topic]; exists {
			return nil, fmt.Errorf("kafka metric topic %q is duplicated", topic)
		}
		allowed[topic] = struct{}{}
	}
	registerer = prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer)
	metrics := &Metrics{
		topics: allowed,
		records: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "records_total",
			Help:      "Kafka records observed by direction, allowlisted topic, and bounded outcome.",
		}, []string{"direction", "topic", "outcome"}),
		handlerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "handler_duration_seconds",
			Help:      "Kafka consumer handler or outbox attempt duration by allowlisted topic and bounded outcome.",
			Buckets:   []float64{0.001, 0.003, 0.01, 0.03, 0.1, 0.3, 1, 3, 10},
		}, []string{"topic", "outcome"}),
		consumerLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "consumer_lag_seconds",
			Help:      "Observed age of the most recently handled Kafka record, capped at thirty days.",
		}, []string{"topic"}),
		lastObserved: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "consumer_last_observed_timestamp_seconds",
			Help:      "Unix timestamp of the latest Kafka consumer processing observation.",
		}, []string{"topic"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "retries_total",
			Help:      "Kafka retries by allowlisted topic and bounded processing stage.",
		}, []string{"topic", "stage"}),
		deadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "vpn_platform",
			Subsystem: "kafka",
			Name:      "dead_letters_total",
			Help:      "Kafka records durably dead-lettered by allowlisted source topic.",
		}, []string{"topic"}),
	}
	registerer.MustRegister(metrics.records, metrics.handlerDuration, metrics.consumerLag, metrics.lastObserved, metrics.retries, metrics.deadLetters)
	return metrics, nil
}

func (m *Metrics) OnProduceRecordUnbuffered(record *kgo.Record, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.records.WithLabelValues("produce", m.topic(record.Topic), outcome).Inc()
}

func (m *Metrics) OnFetchRecordUnbuffered(record *kgo.Record, polled bool) {
	if !polled {
		return
	}
	m.records.WithLabelValues("consume", m.topic(record.Topic), "fetched").Inc()
}

func (m *Metrics) ObserveHandler(topic, outcome string, startedAt, recordTimestamp time.Time) {
	topic = m.topic(topic)
	outcome = boundedHandlerOutcome(outcome)
	m.handlerDuration.WithLabelValues(topic, outcome).Observe(time.Since(startedAt).Seconds())
	m.records.WithLabelValues("consume", topic, outcome).Inc()
	m.lastObserved.WithLabelValues(topic).SetToCurrentTime()
	if !recordTimestamp.IsZero() {
		age := time.Since(recordTimestamp)
		if age < 0 {
			age = 0
		}
		if age > maxObservedRecordAge {
			age = maxObservedRecordAge
		}
		m.consumerLag.WithLabelValues(topic).Set(age.Seconds())
	}
}

func (m *Metrics) ObserveOutbox(topic, outcome string, startedAt time.Time) {
	m.handlerDuration.WithLabelValues(m.topic(topic), boundedHandlerOutcome(outcome)).Observe(time.Since(startedAt).Seconds())
}

func (m *Metrics) ObserveRetry(topic, stage string) {
	switch stage {
	case "consumer", "commit", "outbox":
	default:
		stage = "other"
	}
	m.retries.WithLabelValues(m.topic(topic), stage).Inc()
}

func (m *Metrics) ObserveDLQ(topic string) {
	m.deadLetters.WithLabelValues(m.topic(topic)).Inc()
}

func (m *Metrics) topic(topic string) string {
	if _, ok := m.topics[topic]; ok {
		return topic
	}
	return "other"
}

func boundedHandlerOutcome(outcome string) string {
	switch outcome {
	case "success", "retry", "dead_letter", "deferred", "commit_error":
		return outcome
	default:
		return "error"
	}
}
