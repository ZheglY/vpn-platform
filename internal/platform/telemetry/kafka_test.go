package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

func TestKafkaTraceContextPropagatesWithoutMessageMetadata(t *testing.T) {
	exporter, restore := installTestTracing(t)
	defer restore()

	record := &kgo.Record{
		Context: context.Background(),
		Topic:   "payment.succeeded.v1",
		Key:     []byte("private-payment-id"),
		Value:   []byte(`{"subscription_url":"https://example.invalid/s/top-secret-token"}`),
	}
	hook := KafkaHook{}
	hook.OnProduceRecordBuffered(record)
	if value := (kafkaHeaderCarrier{record: record}).Get("traceparent"); value == "" {
		t.Fatal("traceparent was not injected")
	}
	hook.OnProduceRecordUnbuffered(record, nil)

	consumerCtx, finishConsumer := StartKafkaConsumer(context.Background(), record)
	if trace.SpanContextFromContext(consumerCtx).TraceID() != trace.SpanContextFromContext(record.Context).TraceID() {
		t.Fatal("consumer did not continue the producer trace")
	}
	finishConsumer(nil)

	for _, span := range exporter.GetSpans() {
		serialized := span.Name
		for _, attr := range span.Attributes {
			serialized += " " + string(attr.Key) + "=" + attr.Value.String()
		}
		for _, forbidden := range []string{"payment.succeeded.v1", "private-payment-id", "top-secret-token", "partition", "offset"} {
			if strings.Contains(serialized, forbidden) {
				t.Fatalf("forbidden Kafka metadata %q leaked into span: %s", forbidden, serialized)
			}
		}
	}
}
