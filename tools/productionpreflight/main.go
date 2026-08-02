package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type environmentConfig struct {
	FormatVersion        int                  `json:"format_version"`
	Environment          string               `json:"environment"`
	SourceCommit         string               `json:"source_commit"`
	ReleaseImages        map[string]string    `json:"release_images"`
	Identity             identityConfig       `json:"identity"`
	Public               publicConfig         `json:"public"`
	ConfigReferences     configReferences     `json:"config_references"`
	CredentialReferences credentialReferences `json:"credential_references"`
	Operations           operationsConfig     `json:"operations"`
}

type identityConfig struct {
	TrustDomain string `json:"trust_domain"`
	Namespace   string `json:"namespace"`
}

type publicConfig struct {
	TelegramWebhookURL  string `json:"telegram_webhook_url"`
	BillingWebhookURL   string `json:"billing_webhook_url"`
	SubscriptionBaseURL string `json:"subscription_base_url"`
	AdminBaseURL        string `json:"admin_base_url"`
	PaymentReturnURL    string `json:"payment_return_url"`
	TermsURL            string `json:"terms_url"`
	SupportURL          string `json:"support_url"`
}

type configReferences struct {
	PostgresTopology string `json:"postgres_topology"`
	KafkaTopology    string `json:"kafka_topology"`
	RedisTopology    string `json:"redis_topology"`
	Registry         string `json:"registry"`
	BackupRepository string `json:"backup_repository"`
	Observability    string `json:"observability"`
	AlertRouting     string `json:"alert_routing"`
	WireGuardNetwork string `json:"wireguard_network"`
	NodeInventory    string `json:"node_inventory"`
	EdgePolicy       string `json:"edge_policy"`
}

type credentialReferences struct {
	Secrets      map[string]string `json:"secrets"`
	Keys         map[string]string `json:"keys"`
	Certificates map[string]string `json:"certificates"`
}

type operationsConfig struct {
	PrimaryOncall         string   `json:"primary_oncall"`
	SecondaryOncall       string   `json:"secondary_oncall"`
	RPOMinutes            int      `json:"rpo_minutes"`
	RTOMinutes            int      `json:"rto_minutes"`
	CanaryPercent         int      `json:"canary_percent"`
	MinimumNodesPerRegion int      `json:"minimum_nodes_per_region"`
	ReservePercent        int      `json:"reserve_percent"`
	Regions               []string `json:"regions"`
}

type imageInventory struct {
	FormatVersion    int               `json:"format_version"`
	SourceRepository string            `json:"source_repository"`
	Toolchain        map[string]string `json:"toolchain"`
	Excluded         []struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	} `json:"excluded_dockerfiles"`
	Images []imageEntry `json:"images"`
}

type imageEntry struct {
	Name       string `json:"name"`
	Dockerfile string `json:"dockerfile"`
	VEX        string `json:"vex,omitempty"`
}

type serviceBindings struct {
	FormatVersion int                 `json:"format_version"`
	DatabaseRoles map[string][]string `json:"database_roles"`
	Services      []serviceBinding    `json:"services"`
}

type serviceBinding struct {
	Name           string   `json:"name"`
	Database       *string  `json:"database"`
	KafkaPrincipal *string  `json:"kafka_principal"`
	ProduceTopics  []string `json:"produce_topics"`
	ConsumeTopics  []string `json:"consume_topics"`
	ConsumerGroup  *string  `json:"consumer_group"`
	MTLSIdentity   string   `json:"mtls_identity"`
}

func main() {
	configPath := flag.String("config", "", "environment configuration JSON")
	environment := flag.String("environment", "", "expected staging or production environment")
	sourceCommit := flag.String("source-commit", "", "expected reviewed 40-character commit")
	inventoryPath := flag.String("inventory", "deploy/release/images.json", "release image inventory")
	bindingsPath := flag.String("bindings", "deploy/production/service-bindings.json", "service identity and access contract")
	flag.Parse()

	if *configPath == "" || *environment == "" || *sourceCommit == "" {
		fmt.Fprintln(os.Stderr, "production preflight failed: --config, --environment, and --source-commit are required")
		os.Exit(2)
	}
	if err := runPreflight(*configPath, *environment, *sourceCommit, *inventoryPath, *bindingsPath); err != nil {
		fmt.Fprintf(os.Stderr, "production preflight failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("production preflight passed for %s at %s; no external connections were attempted\n", *environment, *sourceCommit)
}

func runPreflight(configPath, environment, sourceCommit, inventoryPath, bindingsPath string) error {
	if environment != "staging" && environment != "production" {
		return errors.New("environment must be staging or production")
	}
	if !commitPattern.MatchString(sourceCommit) {
		return errors.New("source commit must be a full lowercase Git commit")
	}
	if err := validateRepositoryState(sourceCommit); err != nil {
		return err
	}

	var inventory imageInventory
	if err := decodeStrictFile(inventoryPath, &inventory); err != nil {
		return fmt.Errorf("release image inventory: %w", err)
	}
	var bindings serviceBindings
	if err := decodeStrictFile(bindingsPath, &bindings); err != nil {
		return fmt.Errorf("service bindings: %w", err)
	}
	if err := validateBindings(bindings); err != nil {
		return err
	}
	var candidate environmentConfig
	if err := decodeStrictFile(configPath, &candidate); err != nil {
		return fmt.Errorf("environment configuration: %w", err)
	}
	return validateEnvironment(candidate, environment, sourceCommit, inventory)
}

func validateRepositoryState(expectedCommit string) error {
	headOutput, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return errors.New("repository HEAD cannot be resolved")
	}
	if strings.TrimSpace(string(headOutput)) != expectedCommit {
		return errors.New("source commit does not match repository HEAD")
	}
	statusOutput, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output()
	if err != nil {
		return errors.New("repository state cannot be inspected")
	}
	if len(strings.TrimSpace(string(statusOutput))) != 0 {
		return errors.New("repository worktree must be clean")
	}
	return nil
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("required file cannot be opened")
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid or unknown JSON field")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values are not allowed")
	}
	return nil
}

func validateEnvironment(candidate environmentConfig, expectedEnvironment, expectedCommit string, inventory imageInventory) error {
	var problems []string
	add := func(field string, valid bool) {
		if !valid {
			problems = append(problems, field)
		}
	}
	add("format_version", candidate.FormatVersion == 1)
	add("environment", candidate.Environment == expectedEnvironment)
	add("source_commit", candidate.SourceCommit == expectedCommit && commitPattern.MatchString(candidate.SourceCommit))

	expectedImages := make(map[string]struct{}, len(inventory.Images))
	for _, image := range inventory.Images {
		if image.Name == "" {
			problems = append(problems, "release_image_inventory")
			continue
		}
		expectedImages[image.Name] = struct{}{}
	}
	add("release_images", inventory.FormatVersion == 1 && len(expectedImages) == 19 && len(candidate.ReleaseImages) == len(expectedImages))
	for name := range expectedImages {
		digest, ok := candidate.ReleaseImages[name]
		add("release_images."+name, ok && digestPattern.MatchString(digest))
	}
	for name := range candidate.ReleaseImages {
		_, ok := expectedImages[name]
		add("release_images."+name, ok)
	}
	add("identity.trust_domain", validTrustDomain(candidate.Identity.TrustDomain))
	add("identity.namespace", candidate.Identity.Namespace == expectedEnvironment)

	publicURLs := map[string]string{
		"public.telegram_webhook_url":  candidate.Public.TelegramWebhookURL,
		"public.billing_webhook_url":   candidate.Public.BillingWebhookURL,
		"public.subscription_base_url": candidate.Public.SubscriptionBaseURL,
		"public.admin_base_url":        candidate.Public.AdminBaseURL,
		"public.payment_return_url":    candidate.Public.PaymentReturnURL,
		"public.terms_url":             candidate.Public.TermsURL,
		"public.support_url":           candidate.Public.SupportURL,
	}
	hosts := make(map[string]string)
	for name, value := range publicURLs {
		parsed, err := validateHTTPSURL(value)
		add(name, err == nil)
		if err == nil {
			hosts[name] = strings.ToLower(parsed.Hostname())
		}
	}
	add("public.host_separation", hosts["public.telegram_webhook_url"] != "" && hosts["public.telegram_webhook_url"] != hosts["public.subscription_base_url"] && hosts["public.telegram_webhook_url"] != hosts["public.admin_base_url"] && hosts["public.subscription_base_url"] != hosts["public.admin_base_url"])

	configRefs := map[string]string{
		"config_references.postgres_topology": candidate.ConfigReferences.PostgresTopology,
		"config_references.kafka_topology":    candidate.ConfigReferences.KafkaTopology,
		"config_references.redis_topology":    candidate.ConfigReferences.RedisTopology,
		"config_references.registry":          candidate.ConfigReferences.Registry,
		"config_references.backup_repository": candidate.ConfigReferences.BackupRepository,
		"config_references.observability":     candidate.ConfigReferences.Observability,
		"config_references.alert_routing":     candidate.ConfigReferences.AlertRouting,
		"config_references.wireguard_network": candidate.ConfigReferences.WireGuardNetwork,
		"config_references.node_inventory":    candidate.ConfigReferences.NodeInventory,
		"config_references.edge_policy":       candidate.ConfigReferences.EdgePolicy,
	}
	for name, value := range configRefs {
		add(name, validOpaqueReference(value, "configref"))
	}
	validateReferenceSet(&problems, "credential_references.secrets", candidate.CredentialReferences.Secrets, "secretref", []string{"database_runtime", "redis_password", "telegram_bot_token", "telegram_webhook_secret", "yookassa_credentials"})
	validateReferenceSet(&problems, "credential_references.keys", candidate.CredentialReferences.Keys, "keyref", []string{"access_credential_encryption", "access_token_hmac", "backup_encryption", "pki_issuing", "reality_private", "wireguard_private"})
	validateReferenceSet(&problems, "credential_references.certificates", candidate.CredentialReferences.Certificates, "certref", []string{"admin_client", "edge_tls", "kafka_clients", "observability_clients", "service_mesh"})

	add("operations.primary_oncall", validOpaqueReference(candidate.Operations.PrimaryOncall, "contactref"))
	add("operations.secondary_oncall", validOpaqueReference(candidate.Operations.SecondaryOncall, "contactref") && candidate.Operations.SecondaryOncall != candidate.Operations.PrimaryOncall)
	add("operations.rpo_minutes", candidate.Operations.RPOMinutes > 0)
	add("operations.rto_minutes", candidate.Operations.RTOMinutes > 0)
	add("operations.canary_percent", candidate.Operations.CanaryPercent >= 1 && candidate.Operations.CanaryPercent <= 25)
	add("operations.minimum_nodes_per_region", candidate.Operations.MinimumNodesPerRegion >= 2)
	add("operations.reserve_percent", candidate.Operations.ReservePercent >= 20 && candidate.Operations.ReservePercent <= 90)
	add("operations.regions", validUniqueValues(candidate.Operations.Regions))

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid fields: %s", strings.Join(unique(problems), ", "))
	}
	return nil
}

func validateBindings(bindings serviceBindings) error {
	expectedDatabases := []string{"access", "admin", "billing", "catalog", "identity", "notification", "provisioning", "subscription"}
	if bindings.FormatVersion != 1 || len(bindings.DatabaseRoles) != len(expectedDatabases) {
		return errors.New("service bindings database role inventory is incomplete")
	}
	for _, database := range expectedDatabases {
		roles, ok := bindings.DatabaseRoles[database]
		if !ok || len(roles) != 4 {
			return errors.New("service bindings database role inventory is incomplete")
		}
		for index, suffix := range []string{"_runtime", "_migrator", "_backup", "_restore"} {
			if !strings.HasSuffix(roles[index], suffix) || !strings.Contains(roles[index], "{environment}") {
				return errors.New("service bindings database roles are invalid")
			}
		}
	}
	expectedServices := map[string]struct{}{"identity-service": {}, "catalog-service": {}, "billing-service": {}, "subscription-service": {}, "access-service": {}, "provisioning-service": {}, "node-agent": {}, "telegram-bot": {}, "notification-service": {}, "admin-service": {}}
	if len(bindings.Services) != len(expectedServices) {
		return errors.New("service bindings service inventory is incomplete")
	}
	seen := make(map[string]struct{}, len(bindings.Services))
	for _, service := range bindings.Services {
		if _, ok := expectedServices[service.Name]; !ok {
			return errors.New("service bindings contain an unknown service")
		}
		if _, duplicate := seen[service.Name]; duplicate {
			return errors.New("service bindings contain a duplicate service")
		}
		seen[service.Name] = struct{}{}
		if !strings.HasPrefix(service.MTLSIdentity, "spiffe://{trust_domain}/ns/{environment}/sa/") {
			return errors.New("service bindings contain an invalid mTLS identity")
		}
		if service.Database != nil {
			if _, ok := bindings.DatabaseRoles[*service.Database]; !ok {
				return errors.New("service bindings reference an unknown database")
			}
		}
		hasKafka := service.KafkaPrincipal != nil
		if hasKafka != (len(service.ProduceTopics)+len(service.ConsumeTopics) > 0) || len(service.ConsumeTopics) > 0 && service.ConsumerGroup == nil {
			return errors.New("service bindings contain inconsistent Kafka permissions")
		}
	}
	return nil
}

func validateReferenceSet(problems *[]string, prefix string, values map[string]string, scheme string, expected []string) {
	if len(values) != len(expected) {
		*problems = append(*problems, prefix)
	}
	for _, name := range expected {
		value, ok := values[name]
		if !ok || !validOpaqueReference(value, scheme) {
			*problems = append(*problems, prefix+"."+name)
		}
	}
	for name := range values {
		found := false
		for _, expectedName := range expected {
			found = found || name == expectedName
		}
		if !found {
			*problems = append(*problems, prefix+"."+name)
		}
	}
}

func validateHTTPSURL(value string) (*url.URL, error) {
	if unsafeValue(value) {
		return nil, errors.New("unsafe URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || unsafeHost(parsed.Hostname()) {
		return nil, errors.New("invalid HTTPS URL")
	}
	return parsed, nil
}

func validOpaqueReference(value, scheme string) bool {
	if unsafeValue(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == scheme && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func unsafeValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return true
	}
	for _, marker := range []string{"<required", "${", "placeholder", "local-compose", "example.invalid", "changeme", "dummy", "fake-", "-----begin"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func unsafeHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".invalid") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

func validTrustDomain(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "vpn-service" || unsafeValue(value) || unsafeHost(value) || strings.ContainsAny(value, "/:@") {
		return false
	}
	return strings.Contains(value, ".")
}

func validUniqueValues(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < 2 || unsafeValue(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func unique(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
