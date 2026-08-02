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
	platformredis "github.com/ZheglY/vpn-platform/internal/platform/redis"
	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	billingclient "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/billing"
	"github.com/ZheglY/vpn-platform/services/telegram-bot/internal/bot"
	catalogclient "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/catalog"
	identityclient "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/identity"
	"github.com/ZheglY/vpn-platform/services/telegram-bot/internal/redisstore"
	telegramclient "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/telegram"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

const serviceName = "telegram-bot"

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

	telemetryRuntime, err := platformtelemetry.Setup(ctx, serviceName, appCfg.Environment)
	if err != nil {
		return err
	}
	defer func() { _ = telemetryRuntime.Shutdown(context.Background()) }()

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

	redisClient, err := platformredis.NewClientForEnvironment(appCfg.Environment, appCfg.RedisAddr, appCfg.RedisPassword, appCfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() {
		_ = redisClient.Close()
	}()

	identityHTTPClient := &http.Client{Timeout: appCfg.OutboundTimeout, Transport: platformtelemetry.WrapHTTPTransport(nil)}
	if appCfg.IdentityAuthMode == "mtls" {
		identityHTTPClient, err = platformhttpclient.NewMutualTLSClient(
			appCfg.IdentityClientCertFile,
			appCfg.IdentityClientKeyFile,
			[]string{appCfg.IdentityServerCAFile},
			appCfg.OutboundTimeout,
		)
		if err != nil {
			return err
		}
	}
	identity, err := identityclient.NewClientWithHTTPClient(appCfg.IdentityBaseURL, identityHTTPClient)
	if err != nil {
		return err
	}
	catalog, err := catalogclient.NewClient(appCfg.CatalogBaseURL, identityHTTPClient)
	if err != nil {
		return err
	}
	billing, err := billingclient.NewClient(appCfg.BillingBaseURL, identityHTTPClient)
	if err != nil {
		return err
	}
	telegram, err := telegramclient.NewClient(appCfg.TelegramAPIBaseURL, appCfg.TelegramBotToken, appCfg.OutboundTimeout)
	if err != nil {
		return err
	}
	stateStore := redisstore.New(redisClient, "telegram", appCfg.DedupeTTL, appCfg.ProcessingTTL, appCfg.FSMTTL)
	rateLimiter := redisstore.NewRateLimiter(redisClient, "telegram", int64(appCfg.WebhookRateLimit), appCfg.WebhookRateWindow)

	registry := observability.NewRegistry()
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux := http.NewServeMux()
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{
		"redis": func(ctx context.Context) error {
			return platformredis.Ping(ctx, redisClient)
		},
		"identity": identity.Ping,
		"catalog":  catalog.Ping,
		"billing":  billing.Ping,
	}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("POST /webhooks/telegram", bot.NewWebhookHandlerWithCommerce(bot.Config{
		WebhookSecret:  appCfg.WebhookSecret,
		ConsentVersion: appCfg.ConsentVersion,
		TermsURL:       appCfg.TermsURL,
		CleanupTimeout: appCfg.DedupeCleanupTimeout,
	}, identity, catalog, billing, telegram, stateStore, stateStore, rateLimiter, logger))

	deliveryHandler := bot.NewDeliveryHandler(stateStore, telegram)
	deliveryAuth := passthrough
	if appCfg.DeliveryAuthMode == "mtls" {
		deliveryAuth = httpauth.RequireService(httpauth.ServicePolicy{
			TrustDomain: appCfg.MTLSTrustDomain, Namespace: appCfg.MTLSNamespace,
			Allowed: []string{"notification-service"},
		})
	}
	internalMux := http.NewServeMux()
	internalMux.Handle("GET /livez", httpserver.LivenessHandler(serviceName+"-internal"))
	internalMux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	internalMux.Handle("GET /metrics", observability.MTLSHandler(registry, appCfg.MTLSTrustDomain, appCfg.MTLSNamespace))
	internalMux.Handle("POST /internal/v1/notifications/{delivery_id}/telegram", deliveryAuth(http.HandlerFunc(deliveryHandler.Deliver)))
	internalHandler := httpserver.Chain(
		internalMux,
		httpserver.RequestID,
		platformtelemetry.HTTPServer,
		httpserver.LimitBody(appCfg.MaxBodyBytes),
		httpserver.Recover(logger),
		httpserver.LogRequests(logger),
		httpMetrics.Middleware,
	)

	handler := httpserver.Chain(
		mux,
		httpserver.RequestID,
		platformtelemetry.HTTPServer,
		httpserver.LimitBody(appCfg.MaxBodyBytes),
		httpserver.Recover(logger),
		httpserver.LogRequests(logger),
		httpMetrics.Middleware,
	)

	srv := httpserver.New(appCfg.HTTP, handler)
	internalSrv := httpserver.New(appCfg.InternalHTTP, internalHandler)
	internalSrv.TLSConfig = appCfg.InternalTLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", appCfg.Environment))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	go func() { errs <- httpserver.Run(runCtx, srv, appCfg.HTTP.ShutdownTimeout, logger) }()
	go func() { errs <- httpserver.Run(runCtx, internalSrv, appCfg.InternalHTTP.ShutdownTimeout, logger) }()
	err = <-errs
	cancel()
	<-errs
	return err
}

func passthrough(next http.Handler) http.Handler { return next }

type appConfig struct {
	Environment            string
	LogLevel               string
	RedisAddr              string
	RedisPassword          string
	RedisDB                int
	IdentityBaseURL        string
	CatalogBaseURL         string
	BillingBaseURL         string
	IdentityAuthMode       string
	DeliveryAuthMode       string
	MTLSTrustDomain        string
	MTLSNamespace          string
	IdentityClientCertFile string
	IdentityClientKeyFile  string
	IdentityServerCAFile   string
	TelegramAPIBaseURL     string
	TelegramBotToken       string
	WebhookSecret          string
	ConsentVersion         string
	TermsURL               string
	OutboundTimeout        time.Duration
	DedupeTTL              time.Duration
	ProcessingTTL          time.Duration
	DedupeCleanupTimeout   time.Duration
	FSMTTL                 time.Duration
	MaxBodyBytes           int64
	WebhookRateLimit       int
	WebhookRateWindow      time.Duration
	HTTP                   httpserver.Config
	InternalHTTP           httpserver.Config
	InternalTLS            *tls.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8081")
	internalHTTPCfg := httpserver.DefaultConfig()
	internalHTTPCfg.Addr = config.String("INTERNAL_HTTP_ADDR", ":8090")
	environment := config.String("APP_ENV", "local")
	fields = append(fields, config.ValidateDeploymentEnvironment(environment)...)

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

	redisAddr, err := config.RequiredString("REDIS_ADDR")
	fields = config.Append(fields, "REDIS_ADDR", err)
	redisPassword, err := config.RequiredString("REDIS_PASSWORD")
	fields = config.Append(fields, "REDIS_PASSWORD", err)
	redisDB, err := config.Int("REDIS_DB", 0)
	fields = config.Append(fields, "REDIS_DB", err)
	identityBaseURL, err := config.RequiredString("IDENTITY_BASE_URL")
	fields = config.Append(fields, "IDENTITY_BASE_URL", err)
	catalogBaseURL, err := config.RequiredString("CATALOG_BASE_URL")
	fields = config.Append(fields, "CATALOG_BASE_URL", err)
	billingBaseURL, err := config.RequiredString("BILLING_BASE_URL")
	fields = config.Append(fields, "BILLING_BASE_URL", err)
	identityAuthMode := config.String("IDENTITY_AUTH_MODE", "mtls")
	if identityAuthMode != "mtls" && identityAuthMode != "dev-insecure" {
		fields = config.Append(fields, "IDENTITY_AUTH_MODE", fmt.Errorf("must be mtls or dev-insecure"))
	}
	if identityAuthMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "IDENTITY_AUTH_MODE", fmt.Errorf("dev-insecure is allowed only for local environment"))
	}
	deliveryAuthMode := config.String("DELIVERY_AUTH_MODE", "mtls")
	if deliveryAuthMode != "mtls" && deliveryAuthMode != "dev-insecure" || deliveryAuthMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "DELIVERY_AUTH_MODE", fmt.Errorf("must be mtls, or dev-insecure in local"))
	}
	var internalTLS *tls.Config
	if deliveryAuthMode == "mtls" {
		serverCertFile, certErr := config.RequiredString("INTERNAL_SERVER_TLS_CERT_FILE")
		fields = config.Append(fields, "INTERNAL_SERVER_TLS_CERT_FILE", certErr)
		serverKeyFile, keyErr := config.RequiredString("INTERNAL_SERVER_TLS_KEY_FILE")
		fields = config.Append(fields, "INTERNAL_SERVER_TLS_KEY_FILE", keyErr)
		clientCAFile, caErr := config.RequiredString("INTERNAL_CLIENT_CA_FILE")
		fields = config.Append(fields, "INTERNAL_CLIENT_CA_FILE", caErr)
		if certErr == nil && keyErr == nil && caErr == nil {
			internalTLS, err = httpserver.NewMutualTLSConfig(serverCertFile, serverKeyFile, []string{clientCAFile})
			fields = config.Append(fields, "INTERNAL_TLS_CONFIG", err)
		}
	}
	if identityAuthMode == "mtls" && err == nil && !strings.HasPrefix(identityBaseURL, "https://") {
		fields = config.Append(fields, "IDENTITY_BASE_URL", fmt.Errorf("must use https when IDENTITY_AUTH_MODE=mtls"))
	}
	if identityAuthMode == "mtls" && (!strings.HasPrefix(catalogBaseURL, "https://") || !strings.HasPrefix(billingBaseURL, "https://")) {
		fields = config.Append(fields, "COMMERCE_BASE_URLS", fmt.Errorf("must use https when IDENTITY_AUTH_MODE=mtls"))
	}
	var identityClientCertFile string
	var identityClientKeyFile string
	var identityServerCAFile string
	if identityAuthMode == "mtls" {
		identityClientCertFile, err = config.RequiredString("IDENTITY_CLIENT_CERT_FILE")
		fields = config.Append(fields, "IDENTITY_CLIENT_CERT_FILE", err)
		identityClientKeyFile, err = config.RequiredString("IDENTITY_CLIENT_KEY_FILE")
		fields = config.Append(fields, "IDENTITY_CLIENT_KEY_FILE", err)
		identityServerCAFile, err = config.RequiredString("IDENTITY_SERVER_CA_FILE")
		fields = config.Append(fields, "IDENTITY_SERVER_CA_FILE", err)
	}
	telegramToken, err := config.RequiredString("TELEGRAM_BOT_TOKEN")
	fields = config.Append(fields, "TELEGRAM_BOT_TOKEN", err)
	webhookSecret, err := config.RequiredString("TELEGRAM_WEBHOOK_SECRET")
	fields = config.Append(fields, "TELEGRAM_WEBHOOK_SECRET", err)
	termsURL, err := config.RequiredString("TERMS_URL")
	fields = config.Append(fields, "TERMS_URL", err)

	outboundTimeout, err := config.Duration("OUTBOUND_TIMEOUT", 5*time.Second)
	fields = config.Append(fields, "OUTBOUND_TIMEOUT", err)
	dedupeTTL, err := config.Duration("TELEGRAM_DEDUPE_TTL", 7*24*time.Hour)
	fields = config.Append(fields, "TELEGRAM_DEDUPE_TTL", err)
	processingTTL, err := config.Duration("TELEGRAM_PROCESSING_TTL", 30*time.Second)
	fields = config.Append(fields, "TELEGRAM_PROCESSING_TTL", err)
	dedupeCleanupTimeout, err := config.Duration("TELEGRAM_DEDUPE_CLEANUP_TIMEOUT", 2*time.Second)
	fields = config.Append(fields, "TELEGRAM_DEDUPE_CLEANUP_TIMEOUT", err)
	fsmTTL, err := config.Duration("TELEGRAM_FSM_TTL", 24*time.Hour)
	fields = config.Append(fields, "TELEGRAM_FSM_TTL", err)
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 1<<20)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	webhookRateLimit, err := config.Int("TELEGRAM_WEBHOOK_RATE_LIMIT", 60)
	fields = config.Append(fields, "TELEGRAM_WEBHOOK_RATE_LIMIT", err)
	if webhookRateLimit <= 0 {
		fields = config.Append(fields, "TELEGRAM_WEBHOOK_RATE_LIMIT", fmt.Errorf("must be positive"))
	}
	webhookRateWindow, err := config.Duration("TELEGRAM_WEBHOOK_RATE_WINDOW", time.Minute)
	fields = config.Append(fields, "TELEGRAM_WEBHOOK_RATE_WINDOW", err)

	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}

	return appConfig{
		Environment:            environment,
		LogLevel:               config.String("LOG_LEVEL", "info"),
		RedisAddr:              redisAddr,
		RedisPassword:          redisPassword,
		RedisDB:                redisDB,
		IdentityBaseURL:        identityBaseURL,
		CatalogBaseURL:         catalogBaseURL,
		BillingBaseURL:         billingBaseURL,
		IdentityAuthMode:       identityAuthMode,
		DeliveryAuthMode:       deliveryAuthMode,
		MTLSTrustDomain:        config.String("MTLS_TRUST_DOMAIN", "vpn-service"),
		MTLSNamespace:          config.String("MTLS_NAMESPACE", environment),
		IdentityClientCertFile: identityClientCertFile,
		IdentityClientKeyFile:  identityClientKeyFile,
		IdentityServerCAFile:   identityServerCAFile,
		TelegramAPIBaseURL:     config.String("TELEGRAM_API_BASE_URL", "https://api.telegram.org"),
		TelegramBotToken:       telegramToken,
		WebhookSecret:          webhookSecret,
		ConsentVersion:         config.String("CONSENT_VERSION", "terms-v1"),
		TermsURL:               termsURL,
		OutboundTimeout:        outboundTimeout,
		DedupeTTL:              dedupeTTL,
		ProcessingTTL:          processingTTL,
		DedupeCleanupTimeout:   dedupeCleanupTimeout,
		FSMTTL:                 fsmTTL,
		MaxBodyBytes:           int64(maxBody),
		WebhookRateLimit:       webhookRateLimit,
		WebhookRateWindow:      webhookRateWindow,
		HTTP:                   httpCfg,
		InternalHTTP:           internalHTTPCfg,
		InternalTLS:            internalTLS,
	}, nil
}

func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
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
