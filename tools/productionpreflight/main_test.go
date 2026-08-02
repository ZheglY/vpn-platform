package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEnvironmentAcceptsCompleteOfflineConfiguration(t *testing.T) {
	inventory := testInventory()
	candidate := validEnvironment(inventory)
	if err := validateEnvironment(candidate, "production", strings.Repeat("a", 40), inventory); err != nil {
		t.Fatalf("validate complete environment: %v", err)
	}
}

func TestValidateEnvironmentRejectsPlaceholdersSecretsAndImageDrift(t *testing.T) {
	inventory := testInventory()
	tests := []struct {
		name   string
		mutate func(*environmentConfig)
		field  string
	}{
		{name: "placeholder", mutate: func(c *environmentConfig) { c.ConfigReferences.KafkaTopology = "configref://<required:kafka>" }, field: "config_references.kafka_topology"},
		{name: "secret value", mutate: func(c *environmentConfig) { c.CredentialReferences.Secrets["redis_password"] = "actual-password" }, field: "credential_references.secrets.redis_password"},
		{name: "missing image", mutate: func(c *environmentConfig) { delete(c.ReleaseImages, "migrate") }, field: "release_images"},
		{name: "mutable tag", mutate: func(c *environmentConfig) { c.ReleaseImages["migrate"] = "registry/migrate:latest" }, field: "release_images.migrate"},
		{name: "unsafe URL", mutate: func(c *environmentConfig) { c.Public.SubscriptionBaseURL = "https://127.0.0.1/s" }, field: "public.subscription_base_url"},
		{name: "local trust domain", mutate: func(c *environmentConfig) { c.Identity.TrustDomain = "vpn-service" }, field: "identity.trust_domain"},
		{name: "wrong namespace", mutate: func(c *environmentConfig) { c.Identity.Namespace = "staging" }, field: "identity.namespace"},
		{name: "same oncall", mutate: func(c *environmentConfig) { c.Operations.SecondaryOncall = c.Operations.PrimaryOncall }, field: "operations.secondary_oncall"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validEnvironment(inventory)
			test.mutate(&candidate)
			err := validateEnvironment(candidate, "production", strings.Repeat("a", 40), inventory)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected %s failure, got %v", test.field, err)
			}
		})
	}
}

func TestCommittedTemplatesAreValidJSONButFailClosed(t *testing.T) {
	inventory := testInventory()
	for _, environment := range []string{"staging", "production"} {
		path := filepath.Join("..", "..", "deploy", "environments", environment+".template.json")
		var candidate environmentConfig
		if err := decodeStrictFile(path, &candidate); err != nil {
			t.Fatalf("decode %s template: %v", environment, err)
		}
		if err := validateEnvironment(candidate, environment, strings.Repeat("a", 40), inventory); err == nil {
			t.Fatalf("%s template unexpectedly passed preflight", environment)
		}
	}
}

func TestDecodeStrictFileRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, content := range []string{`{"format_version":1,"unknown":true}`, `{"format_version":1}{"format_version":1}`} {
		path := filepath.Join(t.TempDir(), "candidate.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var candidate environmentConfig
		if err := decodeStrictFile(path, &candidate); err == nil {
			t.Fatalf("expected strict decode failure for %s", content)
		}
	}
}

func TestValidateBindingsAcceptsCommittedContract(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "production", "service-bindings.json")
	var bindings serviceBindings
	if err := decodeStrictFile(path, &bindings); err != nil {
		t.Fatalf("decode bindings: %v", err)
	}
	if err := validateBindings(bindings); err != nil {
		t.Fatalf("validate bindings: %v", err)
	}
	bindings.DatabaseRoles["access"][3] = "{environment}_access_runtime"
	if err := validateBindings(bindings); err == nil {
		t.Fatal("expected duplicate/incorrect database role type to fail")
	}
}

func TestSchemasAreStrictJSON(t *testing.T) {
	for _, name := range []string{"base.schema.json", "staging.schema.json", "production.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "environments", name))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if document["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("%s does not declare JSON Schema 2020-12", name)
		}
	}
}

func TestCommitPatternsAreFullAndImmutable(t *testing.T) {
	for _, value := range []string{"abc", strings.Repeat("A", 40), strings.Repeat("a", 39), "refs/heads/main"} {
		if commitPattern.MatchString(value) {
			t.Fatalf("unexpected accepted commit %q", value)
		}
	}
	if !commitPattern.MatchString(strings.Repeat("a", 40)) {
		t.Fatal("full lowercase commit was rejected")
	}
}

func validEnvironment(inventory imageInventory) environmentConfig {
	images := make(map[string]string, len(inventory.Images))
	for index, image := range inventory.Images {
		images[image.Name] = "sha256:" + strings.Repeat(string(rune('a'+index%6)), 64)
	}
	return environmentConfig{
		FormatVersion: 1, Environment: "production", SourceCommit: strings.Repeat("a", 40), ReleaseImages: images,
		Identity: identityConfig{TrustDomain: "identity.acme.net", Namespace: "production"},
		Public: publicConfig{
			TelegramWebhookURL: "https://telegram.acme.net/webhooks/telegram", BillingWebhookURL: "https://billing.acme.net/webhooks/yookassa",
			SubscriptionBaseURL: "https://subscription.acme.net", AdminBaseURL: "https://admin.internal.acme.net",
			PaymentReturnURL: "https://portal.acme.net/payment-return", TermsURL: "https://legal.acme.net/terms", SupportURL: "https://support.acme.net/help",
		},
		ConfigReferences: configReferences{
			PostgresTopology: "configref://production/postgres", KafkaTopology: "configref://production/kafka", RedisTopology: "configref://production/redis",
			Registry: "configref://production/registry", BackupRepository: "configref://production/backups", Observability: "configref://production/observability",
			AlertRouting: "configref://production/alerts", WireGuardNetwork: "configref://production/wireguard", NodeInventory: "configref://production/nodes", EdgePolicy: "configref://production/edge",
		},
		CredentialReferences: credentialReferences{
			Secrets:      referenceMap("secretref", []string{"database_runtime", "redis_password", "telegram_bot_token", "telegram_webhook_secret", "yookassa_credentials"}),
			Keys:         referenceMap("keyref", []string{"access_credential_encryption", "access_token_hmac", "backup_encryption", "pki_issuing", "reality_private", "wireguard_private"}),
			Certificates: referenceMap("certref", []string{"admin_client", "edge_tls", "kafka_clients", "observability_clients", "service_mesh"}),
		},
		Operations: operationsConfig{PrimaryOncall: "contactref://operations/primary", SecondaryOncall: "contactref://operations/secondary", RPOMinutes: 15, RTOMinutes: 60, CanaryPercent: 10, MinimumNodesPerRegion: 2, ReservePercent: 25, Regions: []string{"region-a"}},
	}
}

func referenceMap(scheme string, names []string) map[string]string {
	result := make(map[string]string, len(names))
	for _, name := range names {
		result[name] = scheme + "://production/" + name
	}
	return result
}

func testInventory() imageInventory {
	names := []string{"credentialstage", "migrate", "backupctl", "identity-service", "catalog-service", "billing-service", "subscription-service", "access-service", "provisioning-service", "node-agent", "telegram-bot", "notification-service", "admin-service", "admin-cli", "prometheus", "grafana", "otel-collector", "tempo", "loki"}
	var inventory imageInventory
	inventory.FormatVersion = 1
	for _, name := range names {
		inventory.Images = append(inventory.Images, imageEntry{Name: name})
	}
	return inventory
}
