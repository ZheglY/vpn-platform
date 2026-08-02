package redis

import (
	"strings"
	"testing"
)

func TestNewClientForEnvironmentRequiresProductionTLS(t *testing.T) {
	t.Setenv("REDIS_TLS_CA_FILE", "")
	client, err := NewClientForEnvironment("production", "redis.internal:6380", "opaque", 0)
	if client != nil {
		_ = client.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "REDIS_TLS_CA_FILE is required") {
		t.Fatalf("expected fail-closed production TLS error, got %v", err)
	}
}

func TestNewClientForEnvironmentKeepsLocalTransportCompatible(t *testing.T) {
	client, err := NewClientForEnvironment("local", "127.0.0.1:6379", "local", 0)
	if err != nil {
		t.Fatalf("create local client: %v", err)
	}
	_ = client.Close()
}
