package logging

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey string

func TestStdoutCoreDropsInternalTraceContext(t *testing.T) {
	var output bytes.Buffer
	core := newFilteredCore(jsonCore(&output), stdoutFieldAllowed, false)
	logger := zap.New(core).With(Context(context.WithValue(context.Background(), contextKey("secret"), "top-secret-token")))
	logger.Info("request handled", zap.String("route", "GET /s/{token}"))

	text := output.String()
	if strings.Contains(text, "top-secret-token") || strings.Contains(text, otelContextField) {
		t.Fatalf("trace context leaked into stdout: %s", text)
	}
	if !strings.Contains(text, "GET /s/{token}") {
		t.Fatalf("safe field missing from stdout: %s", text)
	}
}

func TestOTLPCoreKeepsOnlyReviewedFields(t *testing.T) {
	var output bytes.Buffer
	core := newFilteredCore(jsonCore(&output), otelFieldAllowed, true)
	logger := zap.New(core)
	logger.Info(
		"http request",
		zap.String("route", "GET /s/{token}"),
		zap.String("request_id", "request-1"),
		zap.String("node_id", "private-node"),
		zap.String("subscription_url", "https://example.invalid/s/top-secret-token"),
	)

	text := output.String()
	if !strings.Contains(text, "GET /s/{token}") || !strings.Contains(text, "request-1") {
		t.Fatalf("reviewed fields missing from OTLP core: %s", text)
	}
	for _, forbidden := range []string{"private-node", "subscription_url", "top-secret-token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("forbidden field %q reached OTLP core: %s", forbidden, text)
		}
	}
}

func TestOTLPCoreDropsUnreviewedMessageBodies(t *testing.T) {
	var output bytes.Buffer
	core := newFilteredCore(jsonCore(&output), otelFieldAllowed, true)
	zap.New(core).Info("subscription token top-secret-token", zap.String("route", "GET /s/{token}"))
	if output.Len() != 0 {
		t.Fatalf("unreviewed message reached OTLP core: %s", output.String())
	}
}

func jsonCore(output *bytes.Buffer) zapcore.Core {
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	return zapcore.NewCore(encoder, zapcore.AddSync(output), zap.DebugLevel)
}
