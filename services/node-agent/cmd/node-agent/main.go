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
	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/node-agent/internal/application"
	"github.com/ZheglY/vpn-platform/services/node-agent/internal/httpapi"
	nodemetrics "github.com/ZheglY/vpn-platform/services/node-agent/internal/metrics"
	"github.com/ZheglY/vpn-platform/services/node-agent/internal/state"
	"github.com/ZheglY/vpn-platform/services/node-agent/internal/xray"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "node-agent"

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
	telemetryRuntime, err := platformtelemetry.Setup(ctx, serviceName, cfg.environment)
	if err != nil {
		return err
	}
	defer func() { _ = telemetryRuntime.Shutdown(context.Background()) }()
	logger, err := logging.New(logging.Config{Environment: cfg.environment, Level: cfg.logLevel})
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()
	privateKey, err := xray.LoadPrivateKey(cfg.realityPrivateKeyFile)
	if err != nil {
		return err
	}
	stateStore, err := state.NewStore(cfg.stateDirectory, cfg.operationJournalLimit)
	if err != nil {
		return err
	}
	registry := observability.NewRegistry()
	nodeMetrics, err := nodemetrics.New(registry, serviceName)
	if err != nil {
		return err
	}
	manager, err := xray.NewManager(xray.ManagerConfig{
		BinaryPath: cfg.xrayBinary, ConfigDirectory: cfg.xrayConfigDirectory,
		ValidateTimeout: cfg.validateTimeout, ReloadTimeout: cfg.reloadTimeout, StartupGrace: cfg.startupGrace,
		Render: xray.RenderConfig{ListenAddress: cfg.vpnListenAddress, ListenPort: cfg.vpnListenPort, RealityTarget: cfg.realityTarget, ServerNames: cfg.realityServerNames, RealityPrivateKey: privateKey, ShortIDs: cfg.realityShortIDs},
	}, nodeMetrics)
	if err != nil {
		return err
	}
	service, err := application.NewService(ctx, cfg.nodeID, buildVersion, stateStore, manager, nodeMetrics)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.reloadTimeout)
		defer cancel()
		if err := service.Close(closeCtx); err != nil {
			logger.Warn("Xray shutdown failed", zap.String("error_type", fmt.Sprintf("%T", err)))
		}
	}()

	api := httpapi.New(service)
	policy := func(allowed ...string) func(http.Handler) http.Handler {
		return httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.trustDomain, Namespace: cfg.namespace, Allowed: allowed})
	}
	managementAuth := policy("provisioning-service")
	healthAuth := policy(cfg.healthCallerIdentity)
	httpMetrics := observability.NewHTTPMetrics(registry, serviceName)
	mux := http.NewServeMux()
	mux.Handle("GET /livez", healthAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	mux.Handle("GET /readyz", healthAuth(http.HandlerFunc(api.Readiness)))
	mux.Handle("GET /version", healthAuth(version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate))))
	mux.Handle("GET /metrics", observability.MTLSHandler(registry, cfg.trustDomain, cfg.namespace))
	mux.Handle("GET /internal/v1/status", managementAuth(http.HandlerFunc(api.Status)))
	mux.Handle("GET /internal/v1/credentials/{credential_id}", managementAuth(http.HandlerFunc(api.CredentialState)))
	mux.Handle("PUT /internal/v1/credentials/{credential_id}", managementAuth(http.HandlerFunc(api.ApplyDesiredState)))
	handler := httpserver.Chain(mux, httpserver.RequestID, platformtelemetry.HTTPServer, httpserver.LimitBody(cfg.maxBodyBytes), httpserver.Recover(logger), httpMetrics.Middleware)
	srv := httpserver.New(cfg.http, handler)
	srv.TLSConfig = cfg.tls
	logger.Info("starting node agent", zap.String("service", serviceName), zap.String("node_id", cfg.nodeID), zap.String("environment", cfg.environment))
	return httpserver.Run(ctx, srv, cfg.http.ShutdownTimeout, logger)
}

type appConfig struct {
	environment, logLevel, nodeID, trustDomain, namespace, healthCallerIdentity string
	stateDirectory, xrayBinary, xrayConfigDirectory                             string
	realityPrivateKeyFile, realityTarget, vpnListenAddress                      string
	realityServerNames, realityShortIDs                                         []string
	vpnListenPort, operationJournalLimit                                        int
	validateTimeout, reloadTimeout, startupGrace                                time.Duration
	maxBodyBytes                                                                int64
	http                                                                        httpserver.Config
	tls                                                                         *tls.Config
}

func loadConfig() (appConfig, error) {
	var fields []config.FieldError
	required := func(name string) string {
		value, err := config.RequiredString(name)
		fields = config.Append(fields, name, err)
		return value
	}
	positiveInt := func(name string, fallback int) int {
		value, err := config.Int(name, fallback)
		fields = config.Append(fields, name, err)
		if value <= 0 {
			fields = config.Append(fields, name, fmt.Errorf("must be positive"))
		}
		return value
	}
	positiveDuration := func(name string, fallback time.Duration) time.Duration {
		value, err := config.Duration(name, fallback)
		fields = config.Append(fields, name, err)
		if value <= 0 {
			fields = config.Append(fields, name, fmt.Errorf("must be positive"))
		}
		return value
	}
	environment := config.String("APP_ENV", "local")
	serverNames := splitCSV(required("XRAY_REALITY_SERVER_NAMES"))
	shortIDs := splitCSV(required("XRAY_REALITY_SHORT_IDS"))
	if len(serverNames) == 0 {
		fields = config.Append(fields, "XRAY_REALITY_SERVER_NAMES", fmt.Errorf("at least one server name is required"))
	}
	if len(shortIDs) == 0 {
		fields = config.Append(fields, "XRAY_REALITY_SHORT_IDS", fmt.Errorf("at least one short id is required"))
	}
	vpnPort := positiveInt("XRAY_LISTEN_PORT", 443)
	if vpnPort > 65535 {
		fields = config.Append(fields, "XRAY_LISTEN_PORT", fmt.Errorf("must be at most 65535"))
	}
	serverCert, serverKey, clientCA := required("SERVER_TLS_CERT_FILE"), required("SERVER_TLS_KEY_FILE"), required("CLIENT_CA_FILE")
	var tlsCfg *tls.Config
	if serverCert != "" && serverKey != "" && clientCA != "" {
		var err error
		tlsCfg, err = httpserver.NewMutualTLSConfig(serverCert, serverKey, []string{clientCA})
		fields = config.Append(fields, "TLS_CONFIG", err)
	}
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8443")
	journalLimit := positiveInt("NODE_OPERATION_JOURNAL_LIMIT", 2048)
	maxBody := positiveInt("HTTP_MAX_BODY_BYTES", 64<<10)
	validateTimeout := positiveDuration("XRAY_VALIDATE_TIMEOUT", 5*time.Second)
	reloadTimeout := positiveDuration("XRAY_RELOAD_TIMEOUT", 5*time.Second)
	startupGrace := positiveDuration("XRAY_STARTUP_GRACE", 500*time.Millisecond)
	result := appConfig{
		environment: environment, logLevel: config.String("LOG_LEVEL", "info"), nodeID: required("NODE_ID"),
		trustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), namespace: config.String("MTLS_NAMESPACE", environment), healthCallerIdentity: required("NODE_HEALTH_CALLER_IDENTITY"),
		stateDirectory: required("NODE_STATE_DIRECTORY"), xrayBinary: required("XRAY_BINARY"), xrayConfigDirectory: required("XRAY_CONFIG_DIRECTORY"),
		realityPrivateKeyFile: required("XRAY_REALITY_PRIVATE_KEY_FILE"), realityTarget: required("XRAY_REALITY_TARGET"), vpnListenAddress: config.String("XRAY_LISTEN_ADDRESS", "0.0.0.0"),
		realityServerNames: serverNames, realityShortIDs: shortIDs, vpnListenPort: vpnPort, operationJournalLimit: journalLimit,
		validateTimeout: validateTimeout, reloadTimeout: reloadTimeout, startupGrace: startupGrace, maxBodyBytes: int64(maxBody), http: httpCfg, tls: tlsCfg,
	}
	if result.trustDomain == "" || result.namespace == "" {
		fields = config.Append(fields, "MTLS_IDENTITY", fmt.Errorf("trust domain and namespace are required"))
	}
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	return result, nil
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
	addr := strings.TrimPrefix(config.String("HTTP_ADDR", ":8443"), ":")
	if _, err := strconv.Atoi(addr); err != nil {
		return 1
	}
	cert, key, ca := os.Getenv("HEALTHCHECK_CLIENT_CERT_FILE"), os.Getenv("HEALTHCHECK_CLIENT_KEY_FILE"), os.Getenv("HEALTHCHECK_CA_FILE")
	client, err := platformhttpclient.NewMutualTLSClient(cert, key, []string{ca}, 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp, err := client.Get("https://127.0.0.1:" + addr + "/readyz")
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
