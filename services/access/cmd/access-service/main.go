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
	"unicode/utf8"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/config"
	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
	platformhttpclient "github.com/ZheglY/vpn-platform/internal/platform/httpclient"
	"github.com/ZheglY/vpn-platform/internal/platform/httpserver"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/internal/platform/logging"
	"github.com/ZheglY/vpn-platform/internal/platform/observability"
	platformredis "github.com/ZheglY/vpn-platform/internal/platform/redis"
	"github.com/ZheglY/vpn-platform/internal/platform/version"
	"github.com/ZheglY/vpn-platform/services/access/internal/application"
	"github.com/ZheglY/vpn-platform/services/access/internal/credential"
	"github.com/ZheglY/vpn-platform/services/access/internal/httpapi"
	accesskafka "github.com/ZheglY/vpn-platform/services/access/internal/kafka"
	accesspostgres "github.com/ZheglY/vpn-platform/services/access/internal/postgres"
	"github.com/ZheglY/vpn-platform/services/access/internal/ratelimit"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "access-service"

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
	store, err := accesspostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	keyring, err := credential.NewKeyring(cfg.CredentialKeyVersion, cfg.CredentialKeys)
	if err != nil {
		return err
	}
	hasher, err := credential.NewTokenHasher(cfg.TokenHMACKey)
	if err != nil {
		return err
	}
	service, err := application.NewService(store, keyring, hasher, cfg.PublicBaseURL)
	if err != nil {
		return err
	}
	redisClient := platformredis.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer func() { _ = redisClient.Close() }()
	if err := platformredis.Ping(ctx, redisClient); err != nil {
		return err
	}
	publicLimiter, err := ratelimit.New(redisClient, hasher, int64(cfg.IPRateLimit), int64(cfg.TokenRateLimit), cfg.RateLimitWindow)
	if err != nil {
		return err
	}
	kafkaClient, err := platformkafka.NewClient(cfg.KafkaBrokers, serviceName,
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics(
			"subscription.activated.v1", "subscription.extended.v1", "subscription.expired.v1", "subscription.revoked.v1",
			"access.provision.succeeded.v1", "access.provision.failed.v1", "access.revoke.succeeded.v1", "access.revoke.failed.v1",
		),
		kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return err
	}
	defer kafkaClient.Close()
	consumer := accesskafka.NewConsumer(kafkaClient, service, store, logger, cfg.WorkerRetryDelay)
	worker := application.NewWorker(store, accesskafka.NewPublisher(kafkaClient), logger, cfg.WorkerPollInterval, cfg.WorkerRetryDelay, cfg.WorkerLease)
	go consumer.Run(ctx)
	go worker.Run(ctx)

	api := httpapi.New(service, cfg.ProfileTitle, cfg.SupportURL, cfg.ProfileUpdateHours, publicLimiter)
	issueAuth, rotateAuth, statusAuth, provisioningAuth := passthrough, passthrough, passthrough, passthrough
	if cfg.InternalAuth == "mtls" {
		policy := func(allowed ...string) func(http.Handler) http.Handler {
			return httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.MTLSTrustDomain, Namespace: cfg.MTLSNamespace, Allowed: allowed})
		}
		issueAuth = policy("telegram-bot")
		rotateAuth = policy("telegram-bot", "admin-cli")
		statusAuth = policy("telegram-bot", "admin-cli")
		provisioningAuth = policy("provisioning-service")
	}
	mux := http.NewServeMux()
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping, "kafka": kafkaClient.Ping, "redis": func(ctx context.Context) error { return platformredis.Ping(ctx, redisClient) }}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.Handler(observability.NewRegistry()))
	mux.Handle("POST /internal/v1/subscriptions/{subscription_id}/subscription-url/issue", issueAuth(http.HandlerFunc(api.IssueSubscriptionURL)))
	mux.Handle("POST /internal/v1/subscriptions/{subscription_id}/subscription-url/rotate", rotateAuth(http.HandlerFunc(api.RotateSubscriptionURL)))
	mux.Handle("GET /internal/v1/subscriptions/{subscription_id}/access", statusAuth(http.HandlerFunc(api.GetAccessStatus)))
	mux.Handle("GET /internal/v1/credentials/{credential_id}/provisioning-material", provisioningAuth(http.HandlerFunc(api.GetProvisioningMaterial)))
	mux.Handle("GET /s/{token}", http.HandlerFunc(api.GetHappSubscription))
	handler := httpserver.Chain(mux, httpserver.RequestID, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger))
	srv := httpserver.New(cfg.HTTP, handler)
	srv.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

func passthrough(next http.Handler) http.Handler { return next }

type appConfig struct {
	Environment          string
	LogLevel             string
	DatabaseURL          string
	KafkaBrokers         []string
	ConsumerGroup        string
	InternalAuth         string
	MTLSTrustDomain      string
	MTLSNamespace        string
	CredentialKeyVersion int
	CredentialKeys       map[int][]byte
	TokenHMACKey         []byte
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	IPRateLimit          int
	TokenRateLimit       int
	RateLimitWindow      time.Duration
	PublicBaseURL        string
	ProfileTitle         string
	SupportURL           string
	ProfileUpdateHours   int
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
	consumerGroup := strings.TrimSpace(config.String("KAFKA_CONSUMER_GROUP", "access-service-v1"))
	if consumerGroup == "" {
		fields = config.Append(fields, "KAFKA_CONSUMER_GROUP", fmt.Errorf("must not be empty"))
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
		serverCert := required("SERVER_TLS_CERT_FILE")
		serverKey := required("SERVER_TLS_KEY_FILE")
		clientCA := required("CLIENT_CA_FILE")
		if serverCert != "" && serverKey != "" && clientCA != "" {
			var err error
			tlsCfg, err = httpserver.NewOptionalMutualTLSConfig(serverCert, serverKey, []string{clientCA})
			fields = config.Append(fields, "TLS_CONFIG", err)
		}
	}
	keyVersion, err := config.Int("ACCESS_CREDENTIAL_KEY_VERSION", 1)
	fields = config.Append(fields, "ACCESS_CREDENTIAL_KEY_VERSION", err)
	if keyVersion <= 0 {
		fields = config.Append(fields, "ACCESS_CREDENTIAL_KEY_VERSION", fmt.Errorf("must be positive"))
	}
	credentialKeys, err := credential.DecodeKeySet(required("ACCESS_CREDENTIAL_KEYS"))
	fields = config.Append(fields, "ACCESS_CREDENTIAL_KEYS", err)
	tokenKey, err := credential.DecodeKey(required("ACCESS_TOKEN_HMAC_KEY_BASE64"))
	fields = config.Append(fields, "ACCESS_TOKEN_HMAC_KEY_BASE64", err)
	if activeKey := credentialKeys[keyVersion]; len(activeKey) == credential.KeyBytes && len(tokenKey) == credential.KeyBytes && string(activeKey) == string(tokenKey) {
		fields = config.Append(fields, "ACCESS_TOKEN_HMAC_KEY_BASE64", fmt.Errorf("must differ from credential encryption key"))
	}
	redisAddr := required("REDIS_ADDR")
	redisPassword := required("REDIS_PASSWORD")
	redisDB, err := config.Int("REDIS_DB", 1)
	fields = config.Append(fields, "REDIS_DB", err)
	if redisDB < 0 {
		fields = config.Append(fields, "REDIS_DB", fmt.Errorf("must not be negative"))
	}
	ipRateLimit, err := config.Int("ACCESS_PUBLIC_IP_RATE_LIMIT", 120)
	fields = config.Append(fields, "ACCESS_PUBLIC_IP_RATE_LIMIT", err)
	if ipRateLimit <= 0 {
		fields = config.Append(fields, "ACCESS_PUBLIC_IP_RATE_LIMIT", fmt.Errorf("must be positive"))
	}
	tokenRateLimit, err := config.Int("ACCESS_PUBLIC_TOKEN_RATE_LIMIT", 30)
	fields = config.Append(fields, "ACCESS_PUBLIC_TOKEN_RATE_LIMIT", err)
	if tokenRateLimit <= 0 {
		fields = config.Append(fields, "ACCESS_PUBLIC_TOKEN_RATE_LIMIT", fmt.Errorf("must be positive"))
	}
	publicBaseURL := required("SUBSCRIPTION_PUBLIC_BASE_URL")
	parsedPublic, parseErr := url.Parse(publicBaseURL)
	if parseErr != nil || parsedPublic.Scheme == "" || parsedPublic.Host == "" || parsedPublic.RawQuery != "" || parsedPublic.Fragment != "" {
		fields = config.Append(fields, "SUBSCRIPTION_PUBLIC_BASE_URL", fmt.Errorf("must be an absolute URL without query or fragment"))
	} else if environment != "local" && parsedPublic.Scheme != "https" {
		fields = config.Append(fields, "SUBSCRIPTION_PUBLIC_BASE_URL", fmt.Errorf("must use https outside local environment"))
	}
	profileTitle := strings.TrimSpace(config.String("HAPP_PROFILE_TITLE", "VPN Platform"))
	if profileTitle == "" || utf8.RuneCountInString(profileTitle) > 25 || strings.ContainsAny(profileTitle, "\r\n") {
		fields = config.Append(fields, "HAPP_PROFILE_TITLE", fmt.Errorf("must contain 1 to 25 characters without newlines"))
	}
	supportURL := strings.TrimSpace(config.String("HAPP_SUPPORT_URL", ""))
	if supportURL != "" {
		support, err := url.Parse(supportURL)
		if err != nil || (support.Scheme != "https" && support.Scheme != "http") || support.Host == "" || strings.ContainsAny(supportURL, "\r\n") {
			fields = config.Append(fields, "HAPP_SUPPORT_URL", fmt.Errorf("must be an absolute HTTP(S) URL"))
		}
	}
	updateHours, err := config.Int("HAPP_PROFILE_UPDATE_HOURS", 6)
	fields = config.Append(fields, "HAPP_PROFILE_UPDATE_HOURS", err)
	if updateHours < 1 || updateHours > 168 {
		fields = config.Append(fields, "HAPP_PROFILE_UPDATE_HOURS", fmt.Errorf("must be between 1 and 168"))
	}
	parseDuration := func(name string, fallback time.Duration) time.Duration {
		value, err := config.Duration(name, fallback)
		fields = config.Append(fields, name, err)
		return value
	}
	poll := parseDuration("ACCESS_WORKER_POLL_INTERVAL", 500*time.Millisecond)
	retry := parseDuration("ACCESS_WORKER_RETRY_DELAY", time.Second)
	lease := parseDuration("ACCESS_WORKER_LEASE", 30*time.Second)
	rateWindow := parseDuration("ACCESS_PUBLIC_RATE_WINDOW", time.Minute)
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	httpCfg := httpserver.DefaultConfig()
	httpCfg.Addr = config.String("HTTP_ADDR", ":8087")
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	return appConfig{
		Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL,
		KafkaBrokers: brokers, ConsumerGroup: consumerGroup, InternalAuth: authMode,
		MTLSTrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), MTLSNamespace: config.String("MTLS_NAMESPACE", environment),
		CredentialKeyVersion: keyVersion, CredentialKeys: credentialKeys, TokenHMACKey: tokenKey,
		RedisAddr: redisAddr, RedisPassword: redisPassword, RedisDB: redisDB, IPRateLimit: ipRateLimit, TokenRateLimit: tokenRateLimit, RateLimitWindow: rateWindow,
		PublicBaseURL: publicBaseURL, ProfileTitle: profileTitle, SupportURL: supportURL, ProfileUpdateHours: updateHours,
		WorkerPollInterval: poll, WorkerRetryDelay: retry, WorkerLease: lease, MaxBodyBytes: int64(maxBody), HTTP: httpCfg, TLS: tlsCfg,
	}, nil
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
	addr := strings.TrimPrefix(config.String("HTTP_ADDR", ":8087"), ":")
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
