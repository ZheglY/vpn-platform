package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/yarik/vpn-service/internal/platform/config"
	"github.com/yarik/vpn-service/internal/platform/httpserver"
	"github.com/yarik/vpn-service/internal/platform/logging"
	"github.com/yarik/vpn-service/internal/platform/observability"
	"github.com/yarik/vpn-service/internal/platform/version"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

const serviceName = "identity-service"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	appCfg, err := loadConfig()
	if err != nil {
		return err
	}

	logger, err := logging.New(logging.Config{
		Environment: appCfg.Environment,
		Level:       appCfg.LogLevel,
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = logger.Sync()
	}()

	registry := observability.NewRegistry()
	mux := http.NewServeMux()
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, nil))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.Handler(registry))

	handler := httpserver.Chain(
		mux,
		httpserver.RequestID,
		httpserver.LimitBody(appCfg.MaxBodyBytes),
		httpserver.Recover(logger),
		httpserver.LogRequests(logger),
	)

	srv := httpserver.New(appCfg.HTTP, handler)
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", appCfg.Environment))
	return httpserver.Run(ctx, srv, appCfg.HTTP.ShutdownTimeout, logger)
}

type appConfig struct {
	Environment  string
	LogLevel     string
	MaxBodyBytes int64
	HTTP         httpserver.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", httpCfg.Addr)

	var err error
	httpCfg.ReadHeaderTimeout, err = config.Duration("HTTP_READ_HEADER_TIMEOUT", httpCfg.ReadHeaderTimeout)
	fields = config.Append(fields, "HTTP_READ_HEADER_TIMEOUT", err)
	httpCfg.ReadTimeout, err = config.Duration("HTTP_READ_TIMEOUT", httpCfg.ReadTimeout)
	fields = config.Append(fields, "HTTP_READ_TIMEOUT", err)
	httpCfg.WriteTimeout, err = config.Duration("HTTP_WRITE_TIMEOUT", httpCfg.WriteTimeout)
	fields = config.Append(fields, "HTTP_WRITE_TIMEOUT", err)
	httpCfg.IdleTimeout, err = config.Duration("HTTP_IDLE_TIMEOUT", httpCfg.IdleTimeout)
	fields = config.Append(fields, "HTTP_IDLE_TIMEOUT", err)
	httpCfg.ShutdownTimeout, err = config.Duration("HTTP_SHUTDOWN_TIMEOUT", httpCfg.ShutdownTimeout)
	fields = config.Append(fields, "HTTP_SHUTDOWN_TIMEOUT", err)

	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 1<<20)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}

	return appConfig{
		Environment:  config.String("APP_ENV", "local"),
		LogLevel:     config.String("LOG_LEVEL", "info"),
		MaxBodyBytes: int64(maxBody),
		HTTP:         httpCfg,
	}, nil
}

func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	addr = strings.TrimPrefix(addr, ":")
	url := "http://127.0.0.1:" + addr + "/livez"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck status: %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
