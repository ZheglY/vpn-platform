package telemetry

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	unmatchedRoute      = "unmatched"
)

func HTTPServer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer(instrumentationName).Start(
			parent,
			"HTTP "+boundedHTTPMethod(r.Method),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", boundedHTTPMethod(r.Method))),
		)
		tracedRequest := r.WithContext(ctx)
		recorder := &traceResponseWriter{ResponseWriter: w, status: http.StatusOK}
		completed := false
		defer func() {
			status := recorder.status
			if !completed {
				status = http.StatusInternalServerError
			}
			route := tracedRequest.Pattern
			if route == "" {
				route = unmatchedRoute
			}
			spanName := route
			if !strings.HasPrefix(route, boundedHTTPMethod(r.Method)+" ") {
				spanName = boundedHTTPMethod(r.Method) + " " + route
			}
			span.SetName(spanName)
			span.SetAttributes(
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status),
			)
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, "")
			}
			span.End()
		}()

		next.ServeHTTP(recorder, tracedRequest)
		completed = true
	})
}

func WrapHTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

func (t roundTripper) Unwrap() http.RoundTripper {
	return t.base
}

func (t roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	method := boundedHTTPMethod(request.Method)
	ctx, span := otel.Tracer(instrumentationName).Start(
		request.Context(),
		"HTTP "+method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("http.request.method", method)),
	)
	defer span.End()

	tracedRequest := request.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(tracedRequest.Header))
	response, err := t.base.RoundTrip(tracedRequest)
	if err != nil {
		span.SetStatus(codes.Error, "")
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, "")
	}
	return response, nil
}

func boundedHTTPMethod(method string) string {
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

type traceResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *traceResponseWriter) WriteHeader(status int) {
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

func (w *traceResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *traceResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
