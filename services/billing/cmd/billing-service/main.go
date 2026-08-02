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
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/billing/internal/application"
	catalogclient "github.com/ZheglY/vpn-platform/services/billing/internal/catalog"
	"github.com/ZheglY/vpn-platform/services/billing/internal/httpapi"
	identityclient "github.com/ZheglY/vpn-platform/services/billing/internal/identity"
	billingkafka "github.com/ZheglY/vpn-platform/services/billing/internal/kafka"
	billingpostgres "github.com/ZheglY/vpn-platform/services/billing/internal/postgres"
	"github.com/ZheglY/vpn-platform/services/billing/internal/yookassa"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "billing-service"

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
	telemetryRuntime, err := platformtelemetry.Setup(ctx, serviceName, cfg.Environment)
	if err != nil {
		return err
	}
	defer func() { _ = telemetryRuntime.Shutdown(context.Background()) }()
	logger, err := logging.New(logging.Config{Environment: cfg.Environment, Level: cfg.LogLevel})
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	registry := observability.NewRegistry()
	store, err := billingpostgres.Open(ctx, cfg.DatabaseURL, platformpostgres.WithMetrics(registry, serviceName))
	if err != nil {
		return err
	}
	defer store.Close()
	if err := observability.RegisterBacklogMetrics(registry, serviceName, store, billingpostgres.BacklogSeries()); err != nil {
		return err
	}
	if err := observability.RegisterStateMetrics(registry, serviceName, store, billingpostgres.StateSeries()); err != nil {
		return err
	}
	if err := store.RegisterBillingMetrics(registry, serviceName); err != nil {
		return err
	}
	internalHTTP := &http.Client{Timeout: cfg.OutboundTimeout, Transport: platformtelemetry.WrapHTTPTransport(nil)}
	if cfg.InternalAuth == "mtls" {
		internalHTTP, err = platformhttpclient.NewMutualTLSClient(cfg.ClientCertFile, cfg.ClientKeyFile, []string{cfg.ServerCAFile}, cfg.OutboundTimeout)
		if err != nil {
			return err
		}
	}
	identity, err := identityclient.NewClient(cfg.IdentityBaseURL, internalHTTP)
	if err != nil {
		return err
	}
	catalog, err := catalogclient.NewClient(cfg.CatalogBaseURL, internalHTTP)
	if err != nil {
		return err
	}
	providerHTTP := &http.Client{
		Timeout:   cfg.ProviderTimeout,
		Transport: platformtelemetry.WrapHTTPTransport(nil),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	provider, err := yookassa.NewClient(cfg.YooKassaBaseURL, cfg.YooKassaShopID, cfg.YooKassaSecretKey, cfg.PaymentReturnURL, providerHTTP)
	if err != nil {
		return err
	}
	kafkaMetrics, err := platformkafka.NewMetrics(registry, serviceName, []string{
		"billing.payment.succeeded.v1", "billing.payment.canceled.v1", "billing.refund.succeeded.v1",
	})
	if err != nil {
		return err
	}
	kafkaClient, err := platformkafka.NewClientForEnvironment(cfg.Environment, cfg.KafkaBrokers, serviceName, kgo.RequiredAcks(kgo.AllISRAcks()), kgo.WithHooks(kafkaMetrics))
	if err != nil {
		return err
	}
	defer kafkaClient.Close()
	service := application.NewService(store, identity, catalog, provider, cfg.YooKassaShopID, cfg.ProviderCreateWindow, cfg.WorkerRetryDelay)
	worker := application.NewWorker(service, store, billingkafka.NewPublisher(kafkaClient), logger, cfg.WorkerPollInterval, cfg.WorkerLease, kafkaMetrics)
	go worker.Run(ctx)
	api := httpapi.New(service, store)
	commerceAuth := func(next http.Handler) http.Handler { return next }
	readAuth := commerceAuth
	if cfg.InternalAuth == "mtls" {
		commerceAuth = httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.MTLSTrustDomain, Namespace: cfg.MTLSNamespace, Allowed: []string{"telegram-bot"}})
		readAuth = httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.MTLSTrustDomain, Namespace: cfg.MTLSNamespace, Allowed: []string{"telegram-bot", "subscription-service", "admin-service"}})
	}
	mux := http.NewServeMux()
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping, "identity": identity.Ping, "catalog": catalog.Ping, "kafka": kafkaClient.Ping}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, cfg.MTLSTrustDomain, cfg.MTLSNamespace))
	mux.Handle("POST /internal/v1/users/{user_id}/orders", commerceAuth(http.HandlerFunc(api.CreateOrder)))
	mux.Handle("GET /internal/v1/users/{user_id}/orders/{order_id}", readAuth(http.HandlerFunc(api.GetOrder)))
	mux.Handle("GET /internal/v1/users/{user_id}/orders/{order_id}/payments/{payment_id}", readAuth(http.HandlerFunc(api.GetPaymentStatus)))
	mux.Handle("POST /internal/v1/users/{user_id}/orders/{order_id}/payments", commerceAuth(http.HandlerFunc(api.CreatePayment)))
	mux.HandleFunc("POST /webhooks/yookassa", api.YooKassaWebhook)
	handler := httpserver.Chain(mux, httpserver.RequestID, platformtelemetry.HTTPServer, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger), httpMetrics.Middleware)
	srv := httpserver.New(cfg.HTTP, handler)
	srv.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

type appConfig struct {
	Environment          string
	LogLevel             string
	DatabaseURL          string
	IdentityBaseURL      string
	CatalogBaseURL       string
	InternalAuth         string
	ClientCertFile       string
	ClientKeyFile        string
	ServerCAFile         string
	MTLSTrustDomain      string
	MTLSNamespace        string
	YooKassaBaseURL      string
	YooKassaShopID       string
	YooKassaSecretKey    string
	PaymentReturnURL     string
	KafkaBrokers         []string
	OutboundTimeout      time.Duration
	ProviderTimeout      time.Duration
	ProviderCreateWindow time.Duration
	WorkerPollInterval   time.Duration
	WorkerRetryDelay     time.Duration
	WorkerLease          time.Duration
	MaxBodyBytes         int64
	HTTP                 httpserver.Config
	TLS                  *tls.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	environment := config.String("APP_ENV", "local")
	fields = append(fields, config.ValidateDeploymentEnvironment(environment)...)
	required := func(name string) string {
		value, err := config.RequiredString(name)
		fields = config.Append(fields, name, err)
		return value
	}
	databaseURL := required("DATABASE_URL")
	identityURL := required("IDENTITY_BASE_URL")
	catalogURL := required("CATALOG_BASE_URL")
	yooURL := required("YOOKASSA_BASE_URL")
	shopID := required("YOOKASSA_SHOP_ID")
	secret := required("YOOKASSA_SECRET_KEY")
	returnURL := required("PAYMENT_RETURN_URL")
	authMode := config.String("INTERNAL_AUTH_MODE", "mtls")
	if authMode != "mtls" && authMode != "dev-insecure" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("must be mtls or dev-insecure"))
	}
	if authMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("dev-insecure is local only"))
	}
	if environment != "local" {
		for name, value := range map[string]string{"IDENTITY_BASE_URL": identityURL, "CATALOG_BASE_URL": catalogURL, "YOOKASSA_BASE_URL": yooURL} {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Scheme != "https" {
				fields = config.Append(fields, name, fmt.Errorf("must use https outside local environment"))
			}
		}
	}
	var clientCert, clientKey, serverCA string
	var tlsCfg *tls.Config
	if authMode == "mtls" {
		clientCert = required("INTERNAL_CLIENT_CERT_FILE")
		clientKey = required("INTERNAL_CLIENT_KEY_FILE")
		serverCA = required("INTERNAL_SERVER_CA_FILE")
		serverCert := required("SERVER_TLS_CERT_FILE")
		serverKey := required("SERVER_TLS_KEY_FILE")
		clientCA := required("CLIENT_CA_FILE")
		if clientCert != "" && clientKey != "" && serverCA != "" && serverCert != "" && serverKey != "" && clientCA != "" {
			var err error
			tlsCfg, err = httpserver.NewOptionalMutualTLSConfig(serverCert, serverKey, []string{clientCA})
			fields = config.Append(fields, "TLS_CONFIG", err)
		}
	}
	parseDuration := func(name string, fallback time.Duration) time.Duration {
		value, err := config.Duration(name, fallback)
		fields = config.Append(fields, name, err)
		return value
	}
	outbound := parseDuration("OUTBOUND_TIMEOUT", 5*time.Second)
	providerTimeout := parseDuration("YOOKASSA_TIMEOUT", 10*time.Second)
	createWindow := parseDuration("YOOKASSA_CREATE_WINDOW", 23*time.Hour)
	if createWindow > 23*time.Hour {
		fields = config.Append(fields, "YOOKASSA_CREATE_WINDOW", fmt.Errorf("must not exceed 23h"))
	}
	poll := parseDuration("BILLING_WORKER_POLL_INTERVAL", 500*time.Millisecond)
	retry := parseDuration("BILLING_WORKER_RETRY_DELAY", time.Second)
	lease := parseDuration("BILLING_WORKER_LEASE", 30*time.Second)
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	brokers := splitCSV(required("KAFKA_BROKERS"))
	if len(brokers) == 0 {
		fields = config.Append(fields, "KAFKA_BROKERS", fmt.Errorf("at least one broker is required"))
	}
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8084")
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	return appConfig{Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL, IdentityBaseURL: identityURL, CatalogBaseURL: catalogURL, InternalAuth: authMode, ClientCertFile: clientCert, ClientKeyFile: clientKey, ServerCAFile: serverCA, MTLSTrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), MTLSNamespace: config.String("MTLS_NAMESPACE", environment), YooKassaBaseURL: yooURL, YooKassaShopID: shopID, YooKassaSecretKey: secret, PaymentReturnURL: returnURL, KafkaBrokers: brokers, OutboundTimeout: outbound, ProviderTimeout: providerTimeout, ProviderCreateWindow: createWindow, WorkerPollInterval: poll, WorkerRetryDelay: retry, WorkerLease: lease, MaxBodyBytes: int64(maxBody), HTTP: httpCfg, TLS: tlsCfg}, nil
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func runHealthcheck() int {
	addr := strings.TrimPrefix(config.String("HTTP_ADDR", ":8084"), ":")
	if _, err := strconv.Atoi(addr); err != nil {
		return 1
	}
	client := &http.Client{Timeout: 2 * time.Second}
	scheme := "http"
	if ca := os.Getenv("HEALTHCHECK_CA_FILE"); ca != "" {
		var err error
		client, err = platformhttpclient.NewTLSClient([]string{ca}, 2*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		scheme = "https"
	}
	resp, err := client.Get(scheme + "://127.0.0.1:" + addr + "/livez")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
