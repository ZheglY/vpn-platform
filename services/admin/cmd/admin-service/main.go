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

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/config"
	platformhttpclient "github.com/ZheglY/vpn-platform/internal/platform/httpclient"
	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
	"github.com/ZheglY/vpn-platform/internal/platform/logging"
	"github.com/ZheglY/vpn-platform/internal/platform/observability"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/admin/internal/application"
	"github.com/ZheglY/vpn-platform/services/admin/internal/httpapi"
	"github.com/ZheglY/vpn-platform/services/admin/internal/owner"
	adminpostgres "github.com/ZheglY/vpn-platform/services/admin/internal/postgres"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "admin-service"

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
	store, err := adminpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	outbound, err := platformhttpclient.NewMutualTLSClient(cfg.ClientCertFile, cfg.ClientKeyFile, []string{cfg.ServerCAFile}, cfg.OutboundTimeout)
	if err != nil {
		return err
	}
	owners, err := owner.New(cfg.Owners, outbound)
	if err != nil {
		return err
	}
	service := application.New(store)
	api := httpapi.New(service, owners, cfg.TrustDomain, cfg.Environment)
	mux := http.NewServeMux()
	registry := observability.NewRegistry()
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, cfg.TrustDomain, cfg.Environment))
	mux.Handle("GET /admin/v1/users/{user_id}", api.Protect("identity.read", api.GetIdentity))
	mux.Handle("GET /admin/v1/users/{user_id}/consents/{document_type}/{document_version}", api.Protect("consent.read", api.GetConsent))
	mux.Handle("GET /admin/v1/users/{user_id}/orders/{order_id}", api.Protect("billing.read", api.GetOrder))
	mux.Handle("GET /admin/v1/users/{user_id}/orders/{order_id}/payments/{payment_id}", api.Protect("billing.read", api.GetPayment))
	mux.Handle("GET /admin/v1/users/{user_id}/subscription", api.Protect("subscription.read", api.GetSubscription))
	mux.Handle("GET /admin/v1/subscriptions/{subscription_id}/access", api.Protect("access.read", api.GetAccess))
	mux.Handle("GET /admin/v1/notifications/{notification_id}", api.Protect("notification.read", api.GetNotification))
	mux.Handle("GET /admin/v1/notification-dead-letters", api.Protect("notification.dlq.read", api.ListNotificationDeadLetters))
	mux.Handle("GET /admin/v1/credentials/{credential_id}/provisioning", api.Protect("provisioning.read", api.GetProvisioning))
	mux.Handle("GET /admin/v1/health", api.Protect("health.read", api.GetHealthSummary))
	mux.Handle("POST /admin/v1/notifications/{notification_id}/retry", api.Protect("notification.retry", api.RetryNotification))
	mux.Handle("POST /admin/v1/subscriptions/{subscription_id}/revoke", api.Protect("subscription.revoke", api.RevokeSubscription))
	mux.Handle("POST /admin/v1/credentials/{credential_id}/recover", api.Protect("access.provisioning.recover", api.RecoverAccess))
	mux.Handle("GET /admin/v1/audit", api.Protect("audit.read", api.ListAudit))
	handler := httpserver.Chain(mux, httpserver.RequestID, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger), httpMetrics.Middleware)
	server := httpserver.New(cfg.HTTP, handler)
	server.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, server, cfg.HTTP.ShutdownTimeout, logger)
}

type appConfig struct {
	Environment     string
	LogLevel        string
	DatabaseURL     string
	TrustDomain     string
	ClientCertFile  string
	ClientKeyFile   string
	ServerCAFile    string
	OutboundTimeout time.Duration
	MaxBodyBytes    int64
	Owners          owner.Config
	HTTP            httpserver.Config
	TLS             *tls.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	required := func(name string) string {
		value, err := config.RequiredString(name)
		fields = config.Append(fields, name, err)
		return value
	}
	environment := config.String("APP_ENV", "local")
	databaseURL := required("DATABASE_URL")
	owners := owner.Config{
		IdentityBaseURL: required("IDENTITY_BASE_URL"), BillingBaseURL: required("BILLING_BASE_URL"),
		SubscriptionBaseURL: required("SUBSCRIPTION_BASE_URL"), AccessBaseURL: required("ACCESS_BASE_URL"),
		ProvisioningBaseURL: required("PROVISIONING_BASE_URL"), NotificationBaseURL: required("NOTIFICATION_BASE_URL"),
	}
	for name, value := range map[string]string{
		"IDENTITY_BASE_URL": owners.IdentityBaseURL, "BILLING_BASE_URL": owners.BillingBaseURL,
		"SUBSCRIPTION_BASE_URL": owners.SubscriptionBaseURL, "ACCESS_BASE_URL": owners.AccessBaseURL,
		"PROVISIONING_BASE_URL": owners.ProvisioningBaseURL, "NOTIFICATION_BASE_URL": owners.NotificationBaseURL,
	} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			fields = config.Append(fields, name, fmt.Errorf("must be a fixed absolute HTTPS URL"))
		}
	}
	clientCert, clientKey, serverCA := required("CLIENT_TLS_CERT_FILE"), required("CLIENT_TLS_KEY_FILE"), required("SERVER_CA_FILE")
	serverCert, serverKey, clientCA := required("SERVER_TLS_CERT_FILE"), required("SERVER_TLS_KEY_FILE"), required("CLIENT_CA_FILE")
	tlsConfig, err := httpserver.NewMutualTLSConfig(serverCert, serverKey, []string{clientCA})
	fields = config.Append(fields, "TLS_CONFIG", err)
	outboundTimeout, err := config.Duration("ADMIN_OUTBOUND_TIMEOUT", 5*time.Second)
	fields = config.Append(fields, "ADMIN_OUTBOUND_TIMEOUT", err)
	if outboundTimeout <= 0 || outboundTimeout > 30*time.Second {
		fields = config.Append(fields, "ADMIN_OUTBOUND_TIMEOUT", fmt.Errorf("must be between 1ns and 30s"))
	}
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody < 1 || maxBody > 1<<20 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be between 1 and 1048576"))
	}
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	httpConfig := httpserver.DefaultConfig()
	httpConfig.Addr = config.String("HTTP_ADDR", ":8092")
	return appConfig{
		Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL,
		TrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), ClientCertFile: clientCert,
		ClientKeyFile: clientKey, ServerCAFile: serverCA, OutboundTimeout: outboundTimeout,
		MaxBodyBytes: int64(maxBody), Owners: owners, HTTP: httpConfig, TLS: tlsConfig,
	}, nil
}

func runHealthcheck() int {
	port := strings.TrimPrefix(config.String("HTTP_ADDR", ":8092"), ":")
	if _, err := strconv.Atoi(port); err != nil {
		return 1
	}
	client, err := platformhttpclient.NewMutualTLSClient(os.Getenv("HEALTHCHECK_CLIENT_CERT_FILE"), os.Getenv("HEALTHCHECK_CLIENT_KEY_FILE"), []string{os.Getenv("HEALTHCHECK_CA_FILE")}, 2*time.Second)
	if err != nil {
		return 1
	}
	response, err := client.Get("https://127.0.0.1:" + port + "/livez")
	if err != nil {
		return 1
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
