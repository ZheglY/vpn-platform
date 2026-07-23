package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
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
	accessclient "github.com/ZheglY/vpn-platform/services/provisioning/internal/access"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/application"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/httpapi"
	provisioningkafka "github.com/ZheglY/vpn-platform/services/provisioning/internal/kafka"
	nodeagentclient "github.com/ZheglY/vpn-platform/services/provisioning/internal/nodeagent"
	provisioningpostgres "github.com/ZheglY/vpn-platform/services/provisioning/internal/postgres"
	subscriptionclient "github.com/ZheglY/vpn-platform/services/provisioning/internal/subscription"
)

var buildVersion = "dev"
var buildCommit = "none"
var buildDate = "unknown"

const serviceName = "provisioning-service"

var seedUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
var seedKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var seedShortIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{2}){0,8}$`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "healthcheck":
		os.Exit(runHealthcheck())
	case len(os.Args) > 1 && os.Args[1] == "seed":
		err = runSeed(ctx)
	case len(os.Args) > 1 && os.Args[1] == "replay-dlq":
		err = runReplay(ctx, os.Args[2:])
	default:
		err = run(ctx)
	}
	if err != nil {
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
	store, err := provisioningpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	internalHTTP, err := platformhttpclient.NewMutualTLSClient(cfg.ClientCertFile, cfg.ClientKeyFile, []string{cfg.ServerCAFile}, cfg.OutboundTimeout)
	if err != nil {
		return err
	}
	access, err := accessclient.NewClient(cfg.AccessBaseURL, internalHTTP)
	if err != nil {
		return err
	}
	subscription, err := subscriptionclient.NewClient(cfg.SubscriptionBaseURL, internalHTTP)
	if err != nil {
		return err
	}
	agents, err := nodeagentclient.NewClient(internalHTTP, cfg.OutboundTimeout)
	if err != nil {
		return err
	}
	kafkaClient, err := platformkafka.NewClient(cfg.KafkaBrokers, serviceName,
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics("access.provision.request.v1", "access.revoke.request.v1"),
		kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return err
	}
	defer kafkaClient.Close()
	service := application.NewService(store, cfg.MaxAttempts)
	consumer := provisioningkafka.NewConsumer(kafkaClient, service, store, logger, cfg.WorkerRetryDelay)
	operationWorker := application.NewOperationWorker(store, access, subscription, agents, logger, cfg.WorkerPollInterval, cfg.WorkerRetryDelay, cfg.WorkerLease)
	outboxWorker := application.NewOutboxWorker(store, provisioningkafka.NewPublisher(kafkaClient), logger, cfg.WorkerPollInterval, cfg.WorkerRetryDelay, cfg.WorkerLease)
	healthWorker := application.NewHealthWorker(store, agents, logger, cfg.HealthInterval, cfg.HealthStaleAfter)
	reconciliationWorker := application.NewReconciliationWorker(store, access, agents, logger, cfg.ReconciliationInterval, cfg.ReconciliationBatch)
	go healthWorker.Run(ctx)
	go reconciliationWorker.Run(ctx)
	go consumer.Run(ctx)
	go operationWorker.Run(ctx)
	go outboxWorker.Run(ctx)

	mux := http.NewServeMux()
	api := httpapi.New(store)
	adminAuth := httpauth.RequireService(httpauth.ServicePolicy{TrustDomain: cfg.TrustDomain, Namespace: cfg.Environment, Allowed: []string{"admin-service"}})
	mux.Handle("GET /livez", httpserver.LivenessHandler(serviceName))
	mux.Handle("GET /readyz", httpserver.ReadinessHandler(serviceName, map[string]httpserver.Check{"postgres": store.Ping, "access": access.Ping, "subscription": subscription.Ping, "kafka": kafkaClient.Ping}))
	mux.Handle("GET /version", version.Handler(version.New(serviceName, buildVersion, buildCommit, buildDate)))
	mux.Handle("GET /metrics", observability.Handler(observability.NewRegistry()))
	mux.Handle("GET /internal/v1/credentials/{credential_id}/support-status", adminAuth(http.HandlerFunc(api.GetCredentialSupportSnapshot)))
	handler := httpserver.Chain(mux, httpserver.RequestID, httpserver.LimitBody(cfg.MaxBodyBytes), httpserver.Recover(logger), httpserver.LogRequests(logger))
	srv := httpserver.New(cfg.HTTP, handler)
	srv.TLSConfig = cfg.TLS
	logger.Info("starting service", zap.String("service", serviceName), zap.String("environment", cfg.Environment))
	return httpserver.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

func runSeed(ctx context.Context) error {
	databaseURL, err := config.RequiredString("DATABASE_URL")
	if err != nil {
		return err
	}
	seedFile, err := config.RequiredString("PROVISIONING_NODE_SEED_FILE")
	if err != nil {
		return err
	}
	file, err := os.Open(seedFile)
	if err != nil {
		return fmt.Errorf("open provisioning node seed: %w", err)
	}
	defer func() { _ = file.Close() }()
	var seeds []domain.NodeSeed
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&seeds); err != nil || len(seeds) == 0 {
		return fmt.Errorf("decode provisioning node seed")
	}
	if err := validateNodeSeeds(seeds); err != nil {
		return err
	}
	store, err := provisioningpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.SeedNodes(ctx, seeds)
}

func validateNodeSeeds(seeds []domain.NodeSeed) error {
	ids, managementURLs, identities := make(map[string]struct{}), make(map[string]struct{}), make(map[string]struct{})
	for _, seed := range seeds {
		managementURL, managementErr := url.Parse(seed.ManagementURL)
		identity, identityErr := url.Parse(seed.ManagementSPIFFEID)
		valid := seedUUIDPattern.MatchString(seed.ID) && len(seed.Region) >= 2 && len(seed.Region) <= 64 && strings.TrimSpace(seed.Region) == seed.Region &&
			managementErr == nil && managementURL.Scheme == "https" && managementURL.Host != "" && managementURL.User == nil &&
			identityErr == nil && identity.Scheme == "spiffe" && identity.Host != "" && identity.User == nil && identity.RawQuery == "" && identity.Fragment == "" &&
			seed.CapacityLimit >= 1 && seed.CapacityLimit <= 1000000 && seed.ReservePercent >= 20 && seed.ReservePercent <= 90 &&
			seed.PublicAddress != "" && seed.PublicPort >= 1 && seed.PublicPort <= 65535 && seed.ServerName != "" && seedKeyPattern.MatchString(seed.RealityPublicKey) && seedShortIDPattern.MatchString(seed.ShortID) && len(seed.Label) >= 1 && len(seed.Label) <= 64
		if !valid {
			return fmt.Errorf("provisioning node seed is invalid")
		}
		if _, duplicate := ids[seed.ID]; duplicate {
			return fmt.Errorf("provisioning node seed contains duplicate identity")
		}
		if _, duplicate := managementURLs[seed.ManagementURL]; duplicate {
			return fmt.Errorf("provisioning node seed contains duplicate management endpoint")
		}
		if _, duplicate := identities[seed.ManagementSPIFFEID]; duplicate {
			return fmt.Errorf("provisioning node seed contains duplicate SPIFFE identity")
		}
		ids[seed.ID], managementURLs[seed.ManagementURL], identities[seed.ManagementSPIFFEID] = struct{}{}, struct{}{}, struct{}{}
	}
	return nil
}

func runReplay(ctx context.Context, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: provisioning-service replay-dlq <topic> <partition> <offset>")
	}
	if args[0] != "access.provision.request.v1" && args[0] != "access.revoke.request.v1" {
		return fmt.Errorf("DLQ source topic is not replayable")
	}
	partitionValue, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || partitionValue < 0 {
		return fmt.Errorf("invalid DLQ partition")
	}
	offset, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || offset < 0 {
		return fmt.Errorf("invalid DLQ offset")
	}
	databaseURL, err := config.RequiredString("DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := provisioningpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	payloadHash, err := store.RequestDeadLetterReplay(ctx, args[0], int32(partitionValue), offset)
	if err != nil {
		return err
	}
	brokers := splitCSV(config.String("KAFKA_BROKERS", ""))
	client, err := platformkafka.NewClient(brokers, serviceName+"-dlq-replay", kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{args[0]: {int32(partitionValue): kgo.NewOffset().At(offset)}}), kgo.DisableAutoCommit(), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		return err
	}
	defer client.Close()
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		fetches := client.PollRecords(fetchCtx, 1)
		if fetchCtx.Err() != nil {
			return fmt.Errorf("fetch DLQ source record")
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return fmt.Errorf("fetch DLQ source record")
		}
		records := fetches.Records()
		if len(records) == 0 {
			continue
		}
		record := records[0]
		if record.Topic != args[0] || record.Partition != int32(partitionValue) || record.Offset != offset {
			return fmt.Errorf("DLQ source record is no longer available")
		}
		sum := sha256.Sum256(record.Value)
		if hex.EncodeToString(sum[:]) != payloadHash {
			return fmt.Errorf("DLQ source record hash mismatch")
		}
		result := client.ProduceSync(ctx, &kgo.Record{Topic: record.Topic, Key: record.Key, Value: record.Value})
		if result.FirstErr() != nil {
			return fmt.Errorf("republish DLQ source record")
		}
		return store.CompleteDeadLetterReplay(ctx, args[0], int32(partitionValue), offset)
	}
}

type appConfig struct {
	Environment, LogLevel, DatabaseURL, AccessBaseURL, SubscriptionBaseURL string
	TrustDomain                                                            string
	ClientCertFile, ClientKeyFile, ServerCAFile                            string
	KafkaBrokers                                                           []string
	ConsumerGroup                                                          string
	OutboundTimeout, WorkerPollInterval, WorkerRetryDelay, WorkerLease     time.Duration
	HealthInterval, HealthStaleAfter, ReconciliationInterval               time.Duration
	ReconciliationBatch                                                    int
	MaxAttempts                                                            int
	MaxBodyBytes                                                           int64
	HTTP                                                                   httpserver.Config
	TLS                                                                    *tls.Config
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
	accessURL := required("ACCESS_BASE_URL")
	subscriptionURL := required("SUBSCRIPTION_BASE_URL")
	clientCert, clientKey, serverCA := required("INTERNAL_CLIENT_CERT_FILE"), required("INTERNAL_CLIENT_KEY_FILE"), required("INTERNAL_SERVER_CA_FILE")
	serverCert, serverKey := required("SERVER_TLS_CERT_FILE"), required("SERVER_TLS_KEY_FILE")
	var tlsConfig *tls.Config
	if clientCert != "" && clientKey != "" && serverCA != "" && serverCert != "" && serverKey != "" {
		var err error
		tlsConfig, err = httpserver.NewOptionalMutualTLSConfig(serverCert, serverKey, []string{serverCA})
		fields = config.Append(fields, "TLS_CONFIG", err)
	}
	for name, value := range map[string]string{"ACCESS_BASE_URL": accessURL, "SUBSCRIPTION_BASE_URL": subscriptionURL} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" {
			fields = config.Append(fields, name, fmt.Errorf("must use https"))
		}
	}
	parseDuration := func(name string, fallback time.Duration) time.Duration {
		value, err := config.Duration(name, fallback)
		fields = config.Append(fields, name, err)
		if value <= 0 {
			fields = config.Append(fields, name, fmt.Errorf("must be positive"))
		}
		return value
	}
	outbound := parseDuration("OUTBOUND_TIMEOUT", 5*time.Second)
	poll := parseDuration("PROVISIONING_WORKER_POLL_INTERVAL", 250*time.Millisecond)
	retry := parseDuration("PROVISIONING_WORKER_RETRY_DELAY", time.Second)
	lease := parseDuration("PROVISIONING_WORKER_LEASE", 30*time.Second)
	health := parseDuration("PROVISIONING_HEALTH_INTERVAL", 10*time.Second)
	stale := parseDuration("PROVISIONING_HEALTH_STALE_AFTER", 45*time.Second)
	reconciliation := parseDuration("PROVISIONING_RECONCILIATION_INTERVAL", 30*time.Second)
	reconciliationBatch, err := config.Int("PROVISIONING_RECONCILIATION_BATCH", 100)
	fields = config.Append(fields, "PROVISIONING_RECONCILIATION_BATCH", err)
	if reconciliationBatch < 1 || reconciliationBatch > 1000 {
		fields = config.Append(fields, "PROVISIONING_RECONCILIATION_BATCH", fmt.Errorf("must be between 1 and 1000"))
	}
	maxAttempts, err := config.Int("PROVISIONING_MAX_ATTEMPTS", 5)
	fields = config.Append(fields, "PROVISIONING_MAX_ATTEMPTS", err)
	if maxAttempts < 1 || maxAttempts > 32 {
		fields = config.Append(fields, "PROVISIONING_MAX_ATTEMPTS", fmt.Errorf("must be between 1 and 32"))
	}
	maxBody, err := config.Int("HTTP_MAX_BODY_BYTES", 64<<10)
	fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", err)
	if maxBody <= 0 {
		fields = config.Append(fields, "HTTP_MAX_BODY_BYTES", fmt.Errorf("must be positive"))
	}
	brokers := splitCSV(required("KAFKA_BROKERS"))
	if len(brokers) == 0 {
		fields = config.Append(fields, "KAFKA_BROKERS", fmt.Errorf("at least one broker is required"))
	}
	httpConfig := httpserver.DefaultConfig()
	httpConfig.Addr = config.String("HTTP_ADDR", ":8088")
	if err := config.Combine(fields); err != nil {
		return appConfig{}, err
	}
	consumerGroup := strings.TrimSpace(config.String("KAFKA_CONSUMER_GROUP", "provisioning-service-v1"))
	if consumerGroup == "" {
		return appConfig{}, fmt.Errorf("KAFKA_CONSUMER_GROUP must not be empty")
	}
	return appConfig{Environment: environment, LogLevel: config.String("LOG_LEVEL", "info"), DatabaseURL: databaseURL, AccessBaseURL: accessURL, SubscriptionBaseURL: subscriptionURL, TrustDomain: config.String("MTLS_TRUST_DOMAIN", "vpn-service"), ClientCertFile: clientCert, ClientKeyFile: clientKey, ServerCAFile: serverCA, KafkaBrokers: brokers, ConsumerGroup: consumerGroup, OutboundTimeout: outbound, WorkerPollInterval: poll, WorkerRetryDelay: retry, WorkerLease: lease, HealthInterval: health, HealthStaleAfter: stale, ReconciliationInterval: reconciliation, ReconciliationBatch: reconciliationBatch, MaxAttempts: maxAttempts, MaxBodyBytes: int64(maxBody), HTTP: httpConfig, TLS: tlsConfig}, nil
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
	port := strings.TrimPrefix(config.String("HTTP_ADDR", ":8088"), ":")
	client := &http.Client{Timeout: 2 * time.Second}
	scheme := "http"
	if ca := os.Getenv("HEALTHCHECK_CA_FILE"); ca != "" {
		var err error
		client, err = platformhttpclient.NewTLSClient([]string{ca}, 2*time.Second)
		if err != nil {
			return 1
		}
		scheme = "https"
	}
	resp, err := client.Get(scheme + "://127.0.0.1:" + port + "/livez")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
