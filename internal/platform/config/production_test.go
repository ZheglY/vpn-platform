package config

import (
	"strings"
	"testing"
)

func TestValidateDeploymentEnvironmentAllowsLocalDefaults(t *testing.T) {
	if fields := ValidateDeploymentEnvironment("local"); len(fields) != 0 {
		t.Fatalf("local validation returned fields: %v", fields)
	}
}

func TestValidateDeploymentEnvironmentAcceptsSecureProductionProfile(t *testing.T) {
	setSecureDeploymentEnvironment(t, "production")
	if fields := ValidateDeploymentEnvironment("production"); len(fields) != 0 {
		t.Fatalf("production validation returned fields: %v", fields)
	}
}

func TestValidateDeploymentEnvironmentRejectsUnsafeValuesWithoutLeakingThem(t *testing.T) {
	setSecureDeploymentEnvironment(t, "staging")
	t.Setenv("YOOKASSA_SECRET_KEY", "local-compose-super-secret")
	t.Setenv("SUBSCRIPTION_PUBLIC_BASE_URL", "https://example.invalid/s")
	t.Setenv("DATABASE_URL", "postgres://runtime:local-compose-password@127.0.0.1:5432/db?sslmode=disable")
	t.Setenv("KAFKA_BROKERS", "localhost:9092")

	fields := ValidateDeploymentEnvironment("staging")
	err := Combine(fields)
	if err == nil {
		t.Fatal("expected unsafe deployment profile to fail")
	}
	message := err.Error()
	for _, secret := range []string{"local-compose-super-secret", "local-compose-password"} {
		if strings.Contains(message, secret) {
			t.Fatalf("configuration error leaked %q: %s", secret, message)
		}
	}
	for _, name := range []string{"YOOKASSA_SECRET_KEY", "SUBSCRIPTION_PUBLIC_BASE_URL", "DATABASE_URL", "KAFKA_BROKERS"} {
		if !strings.Contains(message, name) {
			t.Fatalf("configuration error does not identify %s: %s", name, message)
		}
	}
}

func TestValidateDeploymentEnvironmentRejectsMissingIdentityAndTLS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("KAFKA_BROKERS", "kafka.internal:9093")
	t.Setenv("REDIS_ADDR", "redis.internal:6380")

	err := Combine(ValidateDeploymentEnvironment("production"))
	if err == nil {
		t.Fatal("expected incomplete production profile to fail")
	}
	for _, name := range []string{"MTLS_TRUST_DOMAIN", "MTLS_NAMESPACE", "KAFKA_TLS_CA_FILE", "KAFKA_TLS_CERT_FILE", "KAFKA_TLS_KEY_FILE", "REDIS_TLS_CA_FILE"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("configuration error does not identify %s: %s", name, err)
		}
	}
}

func setSecureDeploymentEnvironment(t *testing.T, environment string) {
	t.Helper()
	t.Setenv("APP_ENV", environment)
	t.Setenv("MTLS_TRUST_DOMAIN", "corp.example.net")
	t.Setenv("MTLS_NAMESPACE", environment)
	t.Setenv("INTERNAL_AUTH_MODE", "mtls")
	t.Setenv("DATABASE_URL", "postgres://runtime:opaque@postgres.internal:5432/service?sslmode=verify-full")
	t.Setenv("KAFKA_BROKERS", "kafka-a.internal:9093,kafka-b.internal:9093")
	t.Setenv("KAFKA_TLS_CA_FILE", "/run/credentials/kafka/ca.pem")
	t.Setenv("KAFKA_TLS_CERT_FILE", "/run/credentials/kafka/tls.crt")
	t.Setenv("KAFKA_TLS_KEY_FILE", "/run/credentials/kafka/tls.key")
	t.Setenv("REDIS_ADDR", "redis.internal:6380")
	t.Setenv("REDIS_PASSWORD", "opaque-value")
	t.Setenv("REDIS_TLS_CA_FILE", "/run/credentials/redis/ca.pem")
	t.Setenv("SUBSCRIPTION_PUBLIC_BASE_URL", "https://subscription.example.net")
}
