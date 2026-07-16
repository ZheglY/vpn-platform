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
	platformhttpclient "github.com/yarik/vpn-service/internal/platform/httpclient"
	"github.com/yarik/vpn-service/internal/platform/httpserver"
	"github.com/yarik/vpn-service/internal/platform/logging"
	"github.com/yarik/vpn-service/internal/platform/observability"
	platformredis "github.com/yarik/vpn-service/internal/platform/redis"
	"github.com/yarik/vpn-service/internal/platform/version"
	"github.com/yarik/vpn-service/services/telegram-bot/internal/bot"
	identityclient "github.com/yarik/vpn-service/services/telegram-bot/internal/identity"
	"github.com/yarik/vpn-service/services/telegram-bot/internal/redisstore"
	telegramclient "github.com/yarik/vpn-service/services/telegram-bot/internal/telegram"
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

	redisClient := platformredis.NewClient(appCfg.RedisAddr, appCfg.RedisPassword, appCfg.RedisDB)
	defer func() {
		_ = redisClient.Close()
	}()

	identityHTTPClient := &http.Client{Timeout: appCfg.OutboundTimeout}
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
	telegram, err := telegramclient.NewClient(appCfg.TelegramAPIBaseURL, appCfg.TelegramBotToken, appCfg.OutboundTimeout)
	if err != nil {
		return err
	}
	stateStore := redisstore.New(redisClient, "telegram", appCfg.DedupeTTL, appCfg.FSMTTL)

	registry := observability.NewRegistry()
	mux := http.NewServeMux()
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{
		"redis": func(ctx context.Context) error {
			return platformredis.Ping(ctx, redisClient)
		},
		"identity": identity.Ping,
	}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.Handler(registry))
	mux.Handle("POST /webhooks/telegram", bot.NewWebhookHandler(bot.Config{
		WebhookSecret:  appCfg.WebhookSecret,
		ConsentVersion: appCfg.ConsentVersion,
	}, identity, telegram, stateStore, stateStore, logger))

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
	Environment            string
	LogLevel               string
	RedisAddr              string
	RedisPassword          string
	RedisDB                int
	IdentityBaseURL        string
	IdentityAuthMode       string
	IdentityClientCertFile string
	IdentityClientKeyFile  string
	IdentityServerCAFile   string
	TelegramAPIBaseURL     string
	TelegramBotToken       string
	WebhookSecret          string
	ConsentVersion         string
	OutboundTimeout        time.Duration
	DedupeTTL              time.Duration
	FSMTTL                 time.Duration
	MaxBodyBytes           int64
	HTTP                   httpserver.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8081")
	environment := config.String("APP_ENV", "local")

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
	identityAuthMode := config.String("IDENTITY_AUTH_MODE", "mtls")
	if identityAuthMode != "mtls" && identityAuthMode != "dev-insecure" {
		fields = config.Append(fields, "IDENTITY_AUTH_MODE", fmt.Errorf("must be mtls or dev-insecure"))
	}
	if identityAuthMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "IDENTITY_AUTH_MODE", fmt.Errorf("dev-insecure is allowed only for local environment"))
	}
	if identityAuthMode == "mtls" && err == nil && !strings.HasPrefix(identityBaseURL, "https://") {
		fields = config.Append(fields, "IDENTITY_BASE_URL", fmt.Errorf("must use https when IDENTITY_AUTH_MODE=mtls"))
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

	outboundTimeout, err := config.Duration("OUTBOUND_TIMEOUT", 5*time.Second)
	fields = config.Append(fields, "OUTBOUND_TIMEOUT", err)
	dedupeTTL, err := config.Duration("TELEGRAM_DEDUPE_TTL", 7*24*time.Hour)
	fields = config.Append(fields, "TELEGRAM_DEDUPE_TTL", err)
	fsmTTL, err := config.Duration("TELEGRAM_FSM_TTL", 24*time.Hour)
	fields = config.Append(fields, "TELEGRAM_FSM_TTL", err)
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 1<<20)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}

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
		IdentityAuthMode:       identityAuthMode,
		IdentityClientCertFile: identityClientCertFile,
		IdentityClientKeyFile:  identityClientKeyFile,
		IdentityServerCAFile:   identityServerCAFile,
		TelegramAPIBaseURL:     config.String("TELEGRAM_API_BASE_URL", "https://api.telegram.org"),
		TelegramBotToken:       telegramToken,
		WebhookSecret:          webhookSecret,
		ConsentVersion:         config.String("CONSENT_VERSION", "terms-v1"),
		OutboundTimeout:        outboundTimeout,
		DedupeTTL:              dedupeTTL,
		FSMTTL:                 fsmTTL,
		MaxBodyBytes:           int64(maxBody),
		HTTP:                   httpCfg,
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
