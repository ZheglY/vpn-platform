package telemetry

import (
	"context"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type KafkaHook struct{}

func (KafkaHook) OnProduceRecordBuffered(record *kgo.Record) {
	parent := record.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, span := otel.Tracer(instrumentationName).Start(
		parent,
		"kafka publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation.name", "publish"),
		),
	)
	record.Context = ctx
	otel.GetTextMapPropagator().Inject(ctx, kafkaHeaderCarrier{record: record})
	_ = span
}

func (KafkaHook) OnProduceRecordUnbuffered(record *kgo.Record, err error) {
	span := trace.SpanFromContext(record.Context)
	if err != nil {
		span.SetStatus(codes.Error, "")
	}
	span.End()
}

func StartKafkaConsumer(ctx context.Context, record *kgo.Record) (context.Context, func(error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	parent := otel.GetTextMapPropagator().Extract(ctx, kafkaHeaderCarrier{record: record})
	consumerCtx, span := otel.Tracer(instrumentationName).Start(
		parent,
		"kafka process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation.name", "process"),
		),
	)
	return consumerCtx, func(err error) {
		if err != nil {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}
}

type kafkaHeaderCarrier struct {
	record *kgo.Record
}

func (c kafkaHeaderCarrier) Get(key string) string {
	for i := len(c.record.Headers) - 1; i >= 0; i-- {
		if strings.EqualFold(c.record.Headers[i].Key, key) {
			return string(c.record.Headers[i].Value)
		}
	}
	return ""
}

func (c kafkaHeaderCarrier) Set(key, value string) {
	headers := c.record.Headers[:0]
	for _, header := range c.record.Headers {
		if !strings.EqualFold(header.Key, key) {
			headers = append(headers, header)
		}
	}
	c.record.Headers = append(headers, kgo.RecordHeader{Key: strings.ToLower(key), Value: []byte(value)})
}

func (c kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c.record.Headers))
	for _, header := range c.record.Headers {
		keys = append(keys, header.Key)
	}
	return keys
}

var _ propagation.TextMapCarrier = kafkaHeaderCarrier{}
