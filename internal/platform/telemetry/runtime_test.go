package telemetry

import (
	"testing"
)

func TestConfigFromEnvironmentRequiresHTTPSMTLS(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "ca.crt")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "client.crt")
	t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "client.key")
	if _, err := configFromEnvironment(); err == nil {
		t.Fatal("plaintext OTLP endpoint was accepted")
	}
}

func TestConfigFromEnvironmentRejectsInvalidSamplingRatio(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.1")
	if _, err := configFromEnvironment(); err == nil {
		t.Fatal("invalid trace sampling ratio was accepted")
	}
}

func TestSignalEndpointAppendsSignalPath(t *testing.T) {
	tests := map[string]string{
		"https://collector:4318":       "https://collector:4318/v1/logs",
		"https://collector:4318/":      "https://collector:4318/v1/logs",
		"https://collector:4318/otlp/": "https://collector:4318/otlp/v1/logs",
	}
	for base, want := range tests {
		if got := signalEndpoint(base, "/v1/logs"); got != want {
			t.Errorf("signalEndpoint(%q) = %q, want %q", base, got, want)
		}
	}
}
