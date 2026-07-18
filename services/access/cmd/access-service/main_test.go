package main

import (
	"strings"
	"testing"
)

const (
	testCredentialKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	testTokenKey      = "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
)

func TestLoadConfigAllowsLocalHTTPOnlyInLocalMode(t *testing.T) {
	setRequiredConfig(t)
	t.Setenv("SUBSCRIPTION_PUBLIC_BASE_URL", "http://127.0.0.1:8087")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_ENV", "production")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "SUBSCRIPTION_PUBLIC_BASE_URL") {
		t.Fatalf("production HTTP URL error = %v", err)
	}
}

func TestLoadConfigRejectsReusedEncryptionAndHMACKey(t *testing.T) {
	setRequiredConfig(t)
	t.Setenv("ACCESS_TOKEN_HMAC_KEY_BASE64", testCredentialKey)
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "ACCESS_TOKEN_HMAC_KEY_BASE64") {
		t.Fatalf("reused key error = %v", err)
	}
}

func setRequiredConfig(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://unused")
	t.Setenv("KAFKA_BROKERS", "localhost:9092")
	t.Setenv("INTERNAL_AUTH_MODE", "dev-insecure")
	t.Setenv("ACCESS_CREDENTIAL_KEY_VERSION", "1")
	t.Setenv("ACCESS_CREDENTIAL_KEYS", "1:"+testCredentialKey)
	t.Setenv("ACCESS_TOKEN_HMAC_KEY_BASE64", testTokenKey)
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_PASSWORD", "test-password")
	t.Setenv("SUBSCRIPTION_PUBLIC_BASE_URL", "https://subscriptions.example")
}
