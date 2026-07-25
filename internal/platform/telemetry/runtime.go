package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const (
	defaultSampleRatio     = 0.1
	defaultExportTimeout   = 3 * time.Second
	defaultShutdownTimeout = 5 * time.Second
)

type Runtime struct {
	traces          *sdktrace.TracerProvider
	logs            *sdklog.LoggerProvider
	shutdownTimeout time.Duration
}

type runtimeConfig struct {
	endpoint       string
	caFile         string
	clientCertFile string
	clientKeyFile  string
	sampleRatio    float64
	disabled       bool
}

func Setup(ctx context.Context, service, environment string) (*Runtime, error) {
	if strings.TrimSpace(service) == "" || strings.TrimSpace(environment) == "" {
		return nil, fmt.Errorf("telemetry service and environment are required")
	}

	cfg, err := configFromEnvironment()
	if err != nil {
		return nil, err
	}
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if cfg.disabled || cfg.endpoint == "" {
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())))
		global.SetLoggerProvider(noop.NewLoggerProvider())
		return &Runtime{shutdownTimeout: defaultShutdownTimeout}, nil
	}

	tlsConfig, err := loadClientTLS(cfg)
	if err != nil {
		return nil, err
	}
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
		semconv.ServiceNamespace("vpn-platform"),
		semconv.DeploymentEnvironmentName(environment),
	)

	traceExporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpointURL(signalEndpoint(cfg.endpoint, "/v1/traces")),
		otlptracehttp.WithTLSClientConfig(tlsConfig.Clone()),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithTimeout(defaultExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	logExporter, err := otlploghttp.New(
		ctx,
		otlploghttp.WithEndpointURL(signalEndpoint(cfg.endpoint, "/v1/logs")),
		otlploghttp.WithTLSClientConfig(tlsConfig.Clone()),
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
		otlploghttp.WithTimeout(defaultExportTimeout),
	)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.sampleRatio))),
		sdktrace.WithBatcher(
			traceExporter,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithBatchTimeout(time.Second),
			sdktrace.WithExportTimeout(defaultExportTimeout),
		),
	)
	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(
			logExporter,
			sdklog.WithMaxQueueSize(2048),
			sdklog.WithExportInterval(time.Second),
			sdklog.WithExportTimeout(defaultExportTimeout),
		)),
	)
	otel.SetTracerProvider(traceProvider)
	global.SetLoggerProvider(logProvider)

	return &Runtime{
		traces:          traceProvider,
		logs:            logProvider,
		shutdownTimeout: defaultShutdownTimeout,
	}, nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, r.shutdownTimeout)
	defer cancel()

	var errs []error
	if r.logs != nil {
		if err := r.logs.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown telemetry logs: %w", err))
		}
	}
	if r.traces != nil {
		if err := r.traces.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown telemetry traces: %w", err))
		}
	}
	return errors.Join(errs...)
}

func configFromEnvironment() (runtimeConfig, error) {
	disabled, err := strconv.ParseBool(valueOrDefault("OTEL_SDK_DISABLED", "false"))
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("parse OTEL_SDK_DISABLED: %w", err)
	}
	ratio, err := strconv.ParseFloat(valueOrDefault("OTEL_TRACES_SAMPLER_ARG", strconv.FormatFloat(defaultSampleRatio, 'f', -1, 64)), 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return runtimeConfig{}, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be a number between 0 and 1")
	}

	cfg := runtimeConfig{
		endpoint:       strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		caFile:         strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE")),
		clientCertFile: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE")),
		clientKeyFile:  strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_CLIENT_KEY")),
		sampleRatio:    ratio,
		disabled:       disabled,
	}
	if cfg.disabled || cfg.endpoint == "" {
		return cfg, nil
	}
	parsed, err := url.Parse(cfg.endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return runtimeConfig{}, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an HTTPS base URL without credentials, query, or fragment")
	}
	if cfg.caFile == "" || cfg.clientCertFile == "" || cfg.clientKeyFile == "" {
		return runtimeConfig{}, fmt.Errorf("OTLP mTLS CA, client certificate, and client key files are required")
	}
	return cfg, nil
}

func loadClientTLS(cfg runtimeConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.clientCertFile, cfg.clientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load OTLP client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.caFile)
	if err != nil {
		return nil, fmt.Errorf("read OTLP CA certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("OTLP CA file does not contain a PEM certificate")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
	}, nil
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func signalEndpoint(base, signalPath string) string {
	parsed, _ := url.Parse(base)
	parsed.Path = strings.TrimRight(parsed.Path, "/") + signalPath
	parsed.RawPath = ""
	return parsed.String()
}
