package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var deploymentSecretFields = []string{
	"DATABASE_URL",
	"REDIS_PASSWORD",
	"TELEGRAM_BOT_TOKEN",
	"TELEGRAM_WEBHOOK_SECRET",
	"YOOKASSA_SHOP_ID",
	"YOOKASSA_SECRET_KEY",
	"ACCESS_CREDENTIAL_KEYS",
	"ACCESS_CREDENTIAL_KEY_BASE64",
	"ACCESS_TOKEN_HMAC_KEYS",
	"ACCESS_TOKEN_HMAC_KEY_BASE64",
}

var deploymentURLFields = []string{
	"IDENTITY_BASE_URL",
	"CATALOG_BASE_URL",
	"BILLING_BASE_URL",
	"SUBSCRIPTION_BASE_URL",
	"ACCESS_BASE_URL",
	"PROVISIONING_BASE_URL",
	"NOTIFICATION_BASE_URL",
	"TELEGRAM_BOT_BASE_URL",
	"TELEGRAM_API_BASE_URL",
	"YOOKASSA_BASE_URL",
	"PAYMENT_RETURN_URL",
	"SUBSCRIPTION_PUBLIC_BASE_URL",
	"TERMS_URL",
	"HAPP_SUPPORT_URL",
}

// ValidateDeploymentEnvironment applies fail-closed checks that are shared by
// every service process. Errors identify fields only; credential values are
// intentionally never included.
func ValidateDeploymentEnvironment(environment string) []FieldError {
	environment = strings.TrimSpace(environment)
	if environment != "local" && environment != "test" && environment != "staging" && environment != "production" {
		return []FieldError{{Name: "APP_ENV", Err: fmt.Errorf("must be local, test, staging, or production")}}
	}
	if environment != "staging" && environment != "production" {
		return nil
	}

	var fields []FieldError
	require := func(name string) string {
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			fields = Append(fields, name, fmt.Errorf("must be explicitly configured for %s", environment))
			return ""
		}
		return strings.TrimSpace(value)
	}

	if value := require("APP_ENV"); value != "" && value != environment {
		fields = Append(fields, "APP_ENV", fmt.Errorf("must match the loaded environment"))
	}
	trustDomain := require("MTLS_TRUST_DOMAIN")
	if isUnsafeDeploymentValue(trustDomain) || trustDomain == "vpn-service" {
		fields = Append(fields, "MTLS_TRUST_DOMAIN", fmt.Errorf("must be an environment-owned trust domain"))
	}
	namespace := require("MTLS_NAMESPACE")
	if namespace != "" && namespace != environment {
		fields = Append(fields, "MTLS_NAMESPACE", fmt.Errorf("must equal APP_ENV"))
	}

	for _, name := range []string{"INTERNAL_AUTH_MODE", "IDENTITY_AUTH_MODE", "DELIVERY_AUTH_MODE"} {
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "mtls" {
			fields = Append(fields, name, fmt.Errorf("must be mtls in staging and production"))
		}
	}
	if value, ok := os.LookupEnv("XRAY_MANAGER_MODE"); ok && strings.TrimSpace(value) != "systemd" {
		fields = Append(fields, "XRAY_MANAGER_MODE", fmt.Errorf("must be systemd in staging and production"))
	}

	for _, name := range deploymentSecretFields {
		if value, ok := os.LookupEnv(name); ok && isUnsafeDeploymentValue(value) {
			fields = Append(fields, name, fmt.Errorf("contains a development or placeholder credential"))
		}
	}
	for _, name := range deploymentURLFields {
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
			fields = Append(fields, name, validateDeploymentHTTPSURL(value))
		}
	}

	if databaseURL, ok := os.LookupEnv("DATABASE_URL"); ok && strings.TrimSpace(databaseURL) != "" {
		fields = Append(fields, "DATABASE_URL", validateDeploymentDatabaseURL(databaseURL))
	}
	if brokers, ok := os.LookupEnv("KAFKA_BROKERS"); ok && strings.TrimSpace(brokers) != "" {
		for _, broker := range strings.Split(brokers, ",") {
			if err := validateRemoteHostPort(strings.TrimSpace(broker)); err != nil {
				fields = Append(fields, "KAFKA_BROKERS", err)
				break
			}
		}
		for _, name := range []string{"KAFKA_TLS_CA_FILE", "KAFKA_TLS_CERT_FILE", "KAFKA_TLS_KEY_FILE"} {
			fields = Append(fields, name, validateCredentialFileReference(require(name)))
		}
	}
	if redisAddr, ok := os.LookupEnv("REDIS_ADDR"); ok && strings.TrimSpace(redisAddr) != "" {
		fields = Append(fields, "REDIS_ADDR", validateRemoteHostPort(strings.TrimSpace(redisAddr)))
		fields = Append(fields, "REDIS_TLS_CA_FILE", validateCredentialFileReference(require("REDIS_TLS_CA_FILE")))
	}

	return fields
}

func validateDeploymentHTTPSURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("must be an absolute HTTPS URL without userinfo")
	}
	if isUnsafeDeploymentHost(parsed.Hostname()) {
		return fmt.Errorf("must not use a loopback, unspecified, or reserved development host")
	}
	return nil
}

func validateDeploymentDatabaseURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return fmt.Errorf("must be a PostgreSQL URL")
	}
	if isUnsafeDeploymentHost(parsed.Hostname()) {
		return fmt.Errorf("must not use a loopback, unspecified, or reserved development host")
	}
	if parsed.Query().Get("sslmode") != "verify-full" {
		return fmt.Errorf("must set sslmode=verify-full")
	}
	return nil
}

func validateRemoteHostPort(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("must be a host:port endpoint")
	}
	if isUnsafeDeploymentHost(host) {
		return fmt.Errorf("must not use a loopback, unspecified, or reserved development host")
	}
	return nil
}

func validateCredentialFileReference(value string) error {
	if value == "" {
		return nil
	}
	if !(filepath.IsAbs(value) || strings.HasPrefix(value, "/")) || isUnsafeDeploymentValue(value) {
		return fmt.Errorf("must be an absolute environment-owned credential path")
	}
	return nil
}

func isUnsafeDeploymentHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".invalid") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

func isUnsafeDeploymentValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, marker := range []string{"local-compose", "example.invalid", "<required", "${", "placeholder", "changeme", "dummy", "fake-"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	switch value {
	case "default", "example", "fake", "password", "secret", "test", "test-shop", "token":
		return true
	default:
		return false
	}
}
