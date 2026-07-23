package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/config"
	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
	platformhttpclient "github.com/ZheglY/vpn-platform/internal/platform/httpclient"
	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
	"github.com/ZheglY/vpn-platform/internal/platform/logging"
	"github.com/ZheglY/vpn-platform/internal/platform/observability"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/identity/internal/httpapi"
	identitypostgres "github.com/ZheglY/vpn-platform/services/identity/internal/postgres"
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
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	store, err := identitypostgres.Open(ctx, appCfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	api := httpapi.New(store)
	authTelegramBot := internalAuth(appCfg, []string{"telegram-bot"})
	authService := internalAuth(appCfg, []string{"telegram-bot", "billing-service", "admin-service"})
	authConsentRead := internalAuth(appCfg, []string{"telegram-bot", "billing-service", "admin-service"})
	authNotification := internalAuth(appCfg, []string{"notification-service"})

	mux := http.NewServeMux()
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{
		"postgres": store.Ping,
	}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, appCfg.MTLSTrustDomain, appCfg.MTLSNamespace))
	mux.Handle("PUT /internal/v1/telegram-users/{telegram_id}", authTelegramBot(http.HandlerFunc(api.UpsertTelegramIdentity)))
	mux.Handle("GET /internal/v1/users/{user_id}", authService(http.HandlerFunc(api.GetUser)))
	mux.Handle("POST /internal/v1/users/{user_id}/consents", authTelegramBot(http.HandlerFunc(api.AcceptConsent)))
	mux.Handle("GET /internal/v1/users/{user_id}/consents/{document_type}/{document_version}", authConsentRead(http.HandlerFunc(api.HasConsent)))
	mux.Handle("GET /internal/v1/users/{user_id}/notification-target", authNotification(http.HandlerFunc(api.GetNotificationTarget)))

	handler := httpserver.Chain(
		mux,
		httpserver.RequestID,
		httpserver.LimitBody(appCfg.MaxBodyBytes),
		httpserver.Recover(logger),
		httpserver.LogRequests(logger),
		httpMetrics.Middleware,
	)

	srv := httpserver.New(appCfg.HTTP, handler)
	srv.TLSConfig = appCfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", appCfg.Environment))
	return httpserver.Run(ctx, srv, appCfg.HTTP.ShutdownTimeout, logger)
}

type appConfig struct {
	Environment     string
	LogLevel        string
	DatabaseURL     string
	InternalAuth    string
	MTLSTrustDomain string
	MTLSNamespace   string
	MaxBodyBytes    int64
	HTTP            httpserver.Config
	TLS             *tls.Config
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

	databaseURL, err := config.RequiredString("DATABASE_URL")
	fields = config.Append(fields, "DATABASE_URL", err)

	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 1<<20)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}

	environment := config.String("APP_ENV", "local")
	internalAuthMode := config.String("INTERNAL_AUTH_MODE", "mtls")
	if internalAuthMode != "mtls" && internalAuthMode != "dev-insecure" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("must be mtls or dev-insecure"))
	}
	if internalAuthMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("dev-insecure is allowed only for local environment"))
	}
	trustDomain := config.String("MTLS_TRUST_DOMAIN", "vpn-service")
	namespace := config.String("MTLS_NAMESPACE", environment)

	var tlsCfg *tls.Config
	if internalAuthMode == "mtls" {
		certFile, certErr := config.RequiredString("SERVER_TLS_CERT_FILE")
		fields = config.Append(fields, "SERVER_TLS_CERT_FILE", certErr)
		keyFile, keyErr := config.RequiredString("SERVER_TLS_KEY_FILE")
		fields = config.Append(fields, "SERVER_TLS_KEY_FILE", keyErr)
		clientCAFile, caErr := config.RequiredString("CLIENT_CA_FILE")
		fields = config.Append(fields, "CLIENT_CA_FILE", caErr)
		if certErr == nil && keyErr == nil && caErr == nil {
			tlsCfg, err = httpserver.NewMutualTLSConfig(certFile, keyFile, []string{clientCAFile})
			fields = config.Append(fields, "TLS_CONFIG", err)
		}
	}

	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}

	return appConfig{
		Environment:     environment,
		LogLevel:        config.String("LOG_LEVEL", "info"),
		DatabaseURL:     databaseURL,
		InternalAuth:    internalAuthMode,
		MTLSTrustDomain: trustDomain,
		MTLSNamespace:   namespace,
		MaxBodyBytes:    int64(maxBody),
		HTTP:            httpCfg,
		TLS:             tlsCfg,
	}, nil
}

func internalAuth(cfg appConfig, allowed []string) func(http.Handler) http.Handler {
	if cfg.InternalAuth == "dev-insecure" {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return httpauth.RequireService(httpauth.ServicePolicy{
		TrustDomain: cfg.MTLSTrustDomain,
		Namespace:   cfg.MTLSNamespace,
		Allowed:     allowed,
	})
}

func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	addr = strings.TrimPrefix(addr, ":")
	client := &http.Client{Timeout: 2 * time.Second}
	scheme := "http"
	if certFile := os.Getenv("HEALTHCHECK_CLIENT_CERT_FILE"); certFile != "" {
		var err error
		client, err = platformhttpclient.NewMutualTLSClient(
			certFile,
			os.Getenv("HEALTHCHECK_CLIENT_KEY_FILE"),
			[]string{os.Getenv("HEALTHCHECK_CA_FILE")},
			2*time.Second,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck TLS config failed: %v\n", err)
			return 1
		}
		scheme = "https"
	}
	url := scheme + "://127.0.0.1:" + addr + "/livez"
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
