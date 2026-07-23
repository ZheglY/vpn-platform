package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const unmatchedRoute = "unmatched"

type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func NewHTTPMetrics(registerer prometheus.Registerer, service string) *HTTPMetrics {
	registerer = prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer)
	metrics := &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "vpn_platform",
			Subsystem: "http_server",
			Name:      "requests_total",
			Help:      "Total HTTP requests handled by route template, method, and response status class.",
		}, []string{"method", "route", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "vpn_platform",
			Subsystem: "http_server",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration by route template, method, and response status class.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"method", "route", "status_class"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "vpn_platform",
			Subsystem: "http_server",
			Name:      "in_flight_requests",
			Help:      "Current number of HTTP requests being handled.",
		}),
	}
	registerer.MustRegister(metrics.requests, metrics.duration, metrics.inFlight)
	return metrics
}

func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()

		started := time.Now()
		recorder := &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}
		completed := false
		defer func() {
			status := recorder.status
			if !completed {
				status = http.StatusInternalServerError
			}
			route := r.Pattern
			if route == "" {
				route = unmatchedRoute
			}
			statusClass := strconv.Itoa(status/100) + "xx"
			if status < 100 || status > 599 {
				statusClass = "other"
			}
			labels := []string{boundedMethod(r.Method), route, statusClass}
			m.requests.WithLabelValues(labels...).Inc()
			m.duration.WithLabelValues(labels...).Observe(time.Since(started).Seconds())
		}()

		next.ServeHTTP(recorder, r)
		completed = true
	})
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodConnect,
		http.MethodDelete,
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
		http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
