package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPServerRecordsOnlyRouteTemplateAndBoundedHTTPData(t *testing.T) {
	exporter, restore := installTestTracing(t)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	request := httptest.NewRequest(http.MethodGet, "/s/top-secret-token?provider_id=private", nil)
	request.Header.Set("User-Agent", "private-user-agent")
	response := httptest.NewRecorder()

	HTTPServer(mux).ServeHTTP(response, request)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /s/{token}" {
		t.Fatalf("span name = %q", span.Name)
	}
	serialized := span.Name
	for _, attr := range span.Attributes {
		serialized += " " + string(attr.Key) + "=" + attr.Value.String()
	}
	for _, forbidden := range []string{"top-secret-token", "provider_id", "private-user-agent", "url.path", "url.query"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("forbidden value %q leaked into span: %s", forbidden, serialized)
		}
	}
	if !strings.Contains(serialized, "http.route=GET /s/{token}") {
		t.Fatalf("route template missing from span: %s", serialized)
	}
}

func TestHTTPTransportInjectsTraceContextWithoutRecordingURL(t *testing.T) {
	exporter, restore := installTestTracing(t)
	defer restore()

	var traceparent string
	transport := WrapHTTPTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		traceparent = request.Header.Get("traceparent")
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "https://access.invalid/s/top-secret-token?credential=private", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	_ = response.Body.Close()
	if traceparent == "" {
		t.Fatal("traceparent was not injected")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	serialized := spans[0].Name
	for _, attr := range spans[0].Attributes {
		serialized += " " + string(attr.Key) + "=" + attr.Value.String()
	}
	for _, forbidden := range []string{"top-secret-token", "credential=private", "access.invalid", "url."} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("forbidden value %q leaked into client span: %s", forbidden, serialized)
		}
	}
}

func installTestTracing(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	t.Helper()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return exporter, func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
