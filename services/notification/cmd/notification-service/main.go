package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/config"
	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
	platformhttpclient "github.com/ZheglY/vpn-platform/internal/platform/httpclient"
	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/internal/platform/logging"
	"github.com/ZheglY/vpn-platform/internal/platform/observability"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	accessclient "github.com/ZheglY/vpn-platform/services/notification/internal/access"
	"github.com/ZheglY/vpn-platform/services/notification/internal/application"
	"github.com/ZheglY/vpn-platform/services/notification/internal/httpapi"
	identityclient "github.com/ZheglY/vpn-platform/services/notification/internal/identity"
	notificationkafka "github.com/ZheglY/vpn-platform/services/notification/internal/kafka"
	notificationpostgres "github.com/ZheglY/vpn-platform/services/notification/internal/postgres"
	subscriptionclient "github.com/ZheglY/vpn-platform/services/notification/internal/subscription"
	telegramclient "github.com/ZheglY/vpn-platform/services/notification/internal/telegram"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "notification-service"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logger, err := logging.New(logging.Config{Environment: cfg.Environment, Level: cfg.LogLevel})
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	store, err := notificationpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	outbound, err := platformhttpclient.NewMutualTLSClient(cfg.ClientCertFile, cfg.ClientKeyFile, []string{cfg.ServerCAFile}, cfg.OutboundTimeout)
	if err != nil {
		return err
	}
	identity, err := identityclient.NewClient(cfg.IdentityBaseURL, cfg.ConsentDocumentType, cfg.ConsentDocumentVersion, outbound)
	if err != nil {
		return err
	}
	access, err := accessclient.NewClient(cfg.AccessBaseURL, outbound)
	if err != nil {
		return err
	}
	subscription, err := subscriptionclient.NewClient(cfg.SubscriptionBaseURL, outbound)
	if err != nil {
		return err
	}
	telegram, err := telegramclient.NewClient(cfg.TelegramBotBaseURL, outbound)
	if err != nil {
		return err
	}
	kafkaClient, err := platformkafka.NewClient(cfg.KafkaBrokers, serviceName,
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics(
			"billing.payment.succeeded.v1", "billing.refund.succeeded.v1",
			"subscription.activated.v1", "subscription.extended.v1", "subscription.grace.started.v1", "subscription.expired.v1", "subscription.revoked.v1",
			"access.ready.v1", "access.provisioning.failed.v1", "access.revoked.v1",
		),
		kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
	)
	if err != nil {
		return err
	}
	defer kafkaClient.Close()
	service := application.NewService(store, cfg.MaxAttempts)
	consumer := notificationkafka.NewConsumer(kafkaClient, service, store, logger, cfg.RetryBase)
	worker := application.NewWorker(store, identity, subscription, access, telegram, logger, cfg.PollInterval, cfg.RetryBase, cfg.Lease)
	go consumer.Run(ctx)
	go worker.Run(ctx)

	api := httpapi.New(store)
	auth := passthrough
	if cfg.InternalAuth == "mtls" {
		auth = httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.TrustDomain, Namespace: cfg.Namespace, Allowed: []string{"admin-service"}})
	}
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping, "kafka": kafkaClient.Ping}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, cfg.TrustDomain, cfg.Environment))
	mux.Handle("GET /internal/v1/notifications/{notification_id}", auth(http.HandlerFunc(api.GetNotification)))
	mux.Handle("POST /internal/v1/notifications/{notification_id}/retry", auth(http.HandlerFunc(api.RetryNotification)))
	mux.Handle("GET /internal/v1/dead-letters", auth(http.HandlerFunc(api.ListDeadLetters)))
	handler := httpserver.Chain(mux, httpserver.RequestID, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger), httpMetrics.Middleware)
	srv := httpserver.New(cfg.HTTP, handler)
	srv.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

func passthrough(next http.Handler) http.Handler { return next }

type appConfig struct {
	Environment            string
	LogLevel               string
	DatabaseURL            string
	KafkaBrokers           []string
	ConsumerGroup          string
	InternalAuth           string
	TrustDomain            string
	Namespace              string
	IdentityBaseURL        string
	SubscriptionBaseURL    string
	AccessBaseURL          string
	TelegramBotBaseURL     string
	ConsentDocumentType    string
	ConsentDocumentVersion string
	ClientCertFile         string
	ClientKeyFile          string
	ServerCAFile           string
	OutboundTimeout        time.Duration
	PollInterval           time.Duration
	RetryBase              time.Duration
	Lease                  time.Duration
	MaxAttempts            int
	MaxBodyBytes           int64
	HTTP                   httpserver.Config
	TLS                    *tls.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	environment := config.String("APP_ENV", "local")
	required := func(name string) string {
		value, err := config.RequiredString(name)
		fields = config.Append(fields, name, err)
		return value
	}
	databaseURL := required("DATABASE_URL")
	brokers := splitCSV(required("KAFKA_BROKERS"))
	if len(brokers) == 0 {
		fields = config.Append(fields, "KAFKA_BROKERS", fmt.Errorf("at least one broker is required"))
	}
	authMode := config.String("INTERNAL_AUTH_MODE", "mtls")
	if authMode != "mtls" && authMode != "dev-insecure" || authMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("must be mtls, or dev-insecure in local"))
	}
	identityURL := required("IDENTITY_BASE_URL")
	subscriptionURL := required("SUBSCRIPTION_BASE_URL")
	accessURL := required("ACCESS_BASE_URL")
	telegramURL := required("TELEGRAM_BOT_BASE_URL")
	consentType := strings.TrimSpace(config.String("CONSENT_DOCUMENT_TYPE", "terms"))
	consentVersion := required("CONSENT_VERSION")
	if consentType == "" || len(consentType) > 64 || len(consentVersion) > 64 {
		fields = config.Append(fields, "CONSENT_VERSION", fmt.Errorf("consent identity is invalid"))
	}
	for name, value := range map[string]string{"IDENTITY_BASE_URL": identityURL, "SUBSCRIPTION_BASE_URL": subscriptionURL, "ACCESS_BASE_URL": accessURL, "TELEGRAM_BOT_BASE_URL": telegramURL} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || environment != "local" && parsed.Scheme != "https" {
			fields = config.Append(fields, name, fmt.Errorf("must be a fixed absolute HTTPS URL"))
		}
	}
	clientCert, clientKey, serverCA := required("CLIENT_TLS_CERT_FILE"), required("CLIENT_TLS_KEY_FILE"), required("SERVER_CA_FILE")
	serverCert, serverKey, clientCA := required("SERVER_TLS_CERT_FILE"), required("SERVER_TLS_KEY_FILE"), required("CLIENT_CA_FILE")
	var tlsCfg *tls.Config
	if authMode == "mtls" && serverCert != "" && serverKey != "" && clientCA != "" {
		var err error
		tlsCfg, err = httpserver.NewMutualTLSConfig(serverCert, serverKey, []string{clientCA})
		fields = config.Append(fields, "TLS_CONFIG", err)
	}
	parseDuration := func(name string, fallback time.Duration) time.Duration {
		value, err := config.Duration(name, fallback)
		fields = config.Append(fields, name, err)
		if value <= 0 {
			fields = config.Append(fields, name, fmt.Errorf("must be positive"))
		}
		return value
	}
	outboundTimeout := parseDuration("NOTIFICATION_OUTBOUND_TIMEOUT", 5*time.Second)
	poll := parseDuration("NOTIFICATION_WORKER_POLL_INTERVAL", 250*time.Millisecond)
	retry := parseDuration("NOTIFICATION_RETRY_BASE", time.Second)
	lease := parseDuration("NOTIFICATION_WORKER_LEASE", 30*time.Second)
	maxAttempts, err := config.Int("NOTIFICATION_MAX_ATTEMPTS", 8)
	fields = config.Append(fields, "NOTIFICATION_MAX_ATTEMPTS", err)
	if maxAttempts < 1 || maxAttempts > 100 {
		fields = config.Append(fields, "NOTIFICATION_MAX_ATTEMPTS", fmt.Errorf("must be between 1 and 100"))
	}
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody < 1 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8091")
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	return appConfig{
		Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL,
		KafkaBrokers: brokers, ConsumerGroup: config.String("KAFKA_CONSUMER_GROUP", "notification-service-v1"),
		InternalAuth: authMode, TrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), Namespace: config.String("MTLS_NAMESPACE", environment),
		IdentityBaseURL: identityURL, SubscriptionBaseURL: subscriptionURL, AccessBaseURL: accessURL, TelegramBotBaseURL: telegramURL,
		ConsentDocumentType: consentType, ConsentDocumentVersion: consentVersion,
		ClientCertFile: clientCert, ClientKeyFile: clientKey, ServerCAFile: serverCA,
		OutboundTimeout: outboundTimeout, PollInterval: poll, RetryBase: retry, Lease: lease,
		MaxAttempts: maxAttempts, MaxBodyBytes: int64(maxBody), HTTP: httpCfg, TLS: tlsCfg,
	}, nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func runHealthcheck() int {
	port := strings.TrimPrefix(config.String("HTTP_ADDR", ":8091"), ":")
	if _, err := strconv.Atoi(port); err != nil {
		return 1
	}
	client, err := platformhttpclient.NewMutualTLSClient(os.Getenv("HEALTHCHECK_CLIENT_CERT_FILE"), os.Getenv("HEALTHCHECK_CLIENT_KEY_FILE"), []string{os.Getenv("HEALTHCHECK_CA_FILE")}, 2*time.Second)
	if err != nil {
		return 1
	}
	resp, err := client.Get("https://127.0.0.1:" + port + "/livez")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
