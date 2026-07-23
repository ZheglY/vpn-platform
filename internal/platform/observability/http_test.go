package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHTTPMetricsUseRouteTemplateWithoutSensitivePathValues(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(registry, "access-service")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	handler := metrics.Middleware(mux)

	request := httptest.NewRequest(http.MethodGet, "/s/top-secret-subscription-token", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	serialized := gatherText(t, registry)
	if !strings.Contains(serialized, `route="GET /s/{token}"`) {
		t.Fatalf("route template is missing from metrics:\n%s", serialized)
	}
	if strings.Contains(serialized, "top-secret-subscription-token") {
		t.Fatalf("sensitive path value leaked into metrics:\n%s", serialized)
	}
	if !strings.Contains(serialized, `status_class="4xx"`) {
		t.Fatalf("status class is missing from metrics:\n%s", serialized)
	}
}

func TestHTTPMetricsBoundUnknownMethodsAndRoutes(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(registry, "identity-service")
	handler := metrics.Middleware(http.NotFoundHandler())

	request := httptest.NewRequest("CUSTOM-METHOD", "/users/private-user-id", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	serialized := gatherText(t, registry)
	if !strings.Contains(serialized, `method="OTHER"`) || !strings.Contains(serialized, `route="unmatched"`) {
		t.Fatalf("bounded labels are missing from metrics:\n%s", serialized)
	}
	if strings.Contains(serialized, "private-user-id") {
		t.Fatalf("unmatched request path leaked into metrics:\n%s", serialized)
	}
}

func TestHTTPMetricsRecordPanicsAsServerErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(registry, "catalog-service")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	})

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("handler panic was unexpectedly swallowed")
			}
		}()
		metrics.Middleware(mux).ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/panic", nil),
		)
	}()

	serialized := gatherText(t, registry)
	if !strings.Contains(serialized, `route="GET /panic"`) || !strings.Contains(serialized, `status_class="5xx"`) {
		t.Fatalf("panic was not recorded as a server error:\n%s", serialized)
	}
}

func gatherText(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	var builder strings.Builder
	for _, family := range families {
		builder.WriteString(family.GetName())
		builder.WriteByte('\n')
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				builder.WriteString(label.GetName())
				builder.WriteString(`="`)
				builder.WriteString(label.GetValue())
				builder.WriteString(`" `)
			}
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}
