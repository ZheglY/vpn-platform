package kafka

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestMetricsBoundTopicsAndOutcomes(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "access-service", []string{"subscription.activated.v1"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Millisecond)
	metrics.OnProduceRecordUnbuffered(&kgo.Record{Topic: "subscription.activated.v1"}, nil)
	metrics.OnProduceRecordUnbuffered(&kgo.Record{Topic: "private-user-topic"}, errors.New("failed"))
	metrics.OnFetchRecordUnbuffered(&kgo.Record{Topic: "subscription.activated.v1"}, true)
	metrics.ObserveHandler("subscription.activated.v1", "unbounded-secret-outcome", started, time.Now().Add(-time.Minute))
	metrics.ObserveOutbox("private-user-topic", "unbounded-secret-outcome", started)
	metrics.ObserveRetry("private-user-topic", "unbounded-stage")
	metrics.ObserveDLQ("subscription.activated.v1")

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
		`topic="subscription.activated.v1"`,
		`topic="other"`,
		`outcome="error"`,
		`stage="other"`,
		"vpn_platform_kafka_consumer_lag_seconds",
		"vpn_platform_kafka_consumer_last_observed_timestamp_seconds",
	} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("metric output is missing %q:\n%s", expected, serialized)
		}
	}
	for _, forbidden := range []string{"private-user-topic", "unbounded-secret-outcome", "unbounded-stage"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("metric output leaked %q:\n%s", forbidden, serialized)
		}
	}
}

func TestNewMetricsRejectsInvalidTopic(t *testing.T) {
	t.Parallel()
	if _, err := NewMetrics(prometheus.NewRegistry(), "service", []string{"User Secret Topic"}); err == nil {
		t.Fatal("NewMetrics() accepted an invalid topic")
	}
}
