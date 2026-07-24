package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/catalog/internal/domain"
	"github.com/ZheglY/vpn-platform/services/catalog/internal/httpapi"
	catalogpostgres "github.com/ZheglY/vpn-platform/services/catalog/internal/postgres"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "catalog-service"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, len(os.Args) > 1 && os.Args[1] == "seed"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, seedOnly bool) error {
	cfg, err := loadConfig(seedOnly)
	if err != nil {
		return err
	}
	if seedOnly {
		store, err := catalogpostgres.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer store.Close()
		return store.SeedPlan(ctx, cfg.SeedPlan, "telegram")
	}
	logger, err := logging.New(logging.Config{Environment: cfg.Environment, Level: cfg.LogLevel})
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	registry := observability.NewRegistry()
	store, err := catalogpostgres.Open(ctx, cfg.DatabaseURL, platformpostgres.WithMetrics(registry, serviceName))
	if err != nil {
		return err
	}
	defer store.Close()
	api := httpapi.New(store)
	internalAuth := func(next http.Handler) http.Handler { return next }
	if cfg.InternalAuth == "mtls" {
		internalAuth = httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.MTLSTrustDomain, Namespace: cfg.MTLSNamespace, Allowed: []string{"billing-service"}})
	}
	mux := http.NewServeMux()
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, cfg.MTLSTrustDomain, cfg.MTLSNamespace))
	mux.HandleFunc("GET /v1/plans", api.ListPlans)
	mux.Handle("GET /internal/v1/plans/{plan_id}", internalAuth(http.HandlerFunc(api.GetPlan)))
	handler := httpserver.Chain(mux, httpserver.RequestID, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger), httpMetrics.Middleware)
	srv := httpserver.New(cfg.HTTP, handler)
	srv.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
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
	SeedPlan        domain.Plan
}

func loadConfig(seedOnly bool) (appConfig, error) {
	var fields []config.FieldError
	environment := config.String("APP_ENV", "local")
	databaseURL, err := config.RequiredString("DATABASE_URL")
	fields = config.Append(fields, "DATABASE_URL", err)
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	var plan domain.Plan
	if seedOnly {
		amount, amountErr := config.Int("CATALOG_SEED_AMOUNT_MINOR", 0)
		fields = config.Append(fields, "CATALOG_SEED_AMOUNT_MINOR", amountErr)
		duration, durationErr := config.Int("CATALOG_SEED_DURATION_DAYS", 30)
		fields = config.Append(fields, "CATALOG_SEED_DURATION_DAYS", durationErr)
		grace, graceErr := config.Int("CATALOG_SEED_GRACE_HOURS", 24)
		fields = config.Append(fields, "CATALOG_SEED_GRACE_HOURS", graceErr)
		plan = domain.Plan{PlanID: strings.TrimSpace(config.String("CATALOG_SEED_PLAN_ID", "")), Name: strings.TrimSpace(config.String("CATALOG_SEED_NAME", "")), DurationDays: duration, GracePeriodHours: grace, AmountMinor: int64(amount), Currency: strings.ToUpper(strings.TrimSpace(config.String("CATALOG_SEED_CURRENCY", ""))), RegionPolicy: "single_region_with_failover", TrafficPolicy: "no_hard_cap", PrimaryNodes: 1, FailoverNodes: 1, Regions: splitCSV(config.String("CATALOG_SEED_REGIONS", ""))}
		if amountErr == nil && durationErr == nil && graceErr == nil {
			fields = config.Append(fields, "CATALOG_SEED_PLAN", plan.Validate())
		}
	}
	authMode := config.String("INTERNAL_AUTH_MODE", "mtls")
	if authMode != "mtls" && authMode != "dev-insecure" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("must be mtls or dev-insecure"))
	}
	if authMode == "dev-insecure" && environment != "local" {
		fields = config.Append(fields, "INTERNAL_AUTH_MODE", fmt.Errorf("dev-insecure is local only"))
	}
	var tlsCfg *tls.Config
	if authMode == "mtls" {
		certFile, certErr := config.RequiredString("SERVER_TLS_CERT_FILE")
		fields = config.Append(fields, "SERVER_TLS_CERT_FILE", certErr)
		keyFile, keyErr := config.RequiredString("SERVER_TLS_KEY_FILE")
		fields = config.Append(fields, "SERVER_TLS_KEY_FILE", keyErr)
		caFile, caErr := config.RequiredString("CLIENT_CA_FILE")
		fields = config.Append(fields, "CLIENT_CA_FILE", caErr)
		if certErr == nil && keyErr == nil && caErr == nil {
			tlsCfg, err = httpserver.NewOptionalMutualTLSConfig(certFile, keyFile, []string{caFile})
			fields = config.Append(fields, "TLS_CONFIG", err)
		}
	}
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8083")
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	return appConfig{Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL, InternalAuth: authMode, MTLSTrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), MTLSNamespace: config.String("MTLS_NAMESPACE", environment), MaxBodyBytes: int64(maxBody), HTTP: httpCfg, TLS: tlsCfg, SeedPlan: plan}, nil
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
	addr := strings.TrimPrefix(config.String("HTTP_ADDR", ":8083"), ":")
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
