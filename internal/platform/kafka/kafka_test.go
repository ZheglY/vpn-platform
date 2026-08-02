package kafka

import (
	"strings"
	"testing"
)

func TestNewClientRequiresBroker(t *testing.T) {
	if _, err := NewClient(nil, "test"); err == nil {
		t.Fatal("expected error for missing brokers")
	}
}

func TestNewClientForEnvironmentRequiresProductionMTLS(t *testing.T) {
	t.Setenv("KAFKA_TLS_CA_FILE", "")
	t.Setenv("KAFKA_TLS_CERT_FILE", "")
	t.Setenv("KAFKA_TLS_KEY_FILE", "")
	client, err := NewClientForEnvironment("production", []string{"kafka.internal:9093"}, "test")
	if client != nil {
		client.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "mTLS credential files are required") {
		t.Fatalf("expected fail-closed production mTLS error, got %v", err)
	}
}

func TestNewClientForEnvironmentKeepsLocalTransportCompatible(t *testing.T) {
	client, err := NewClientForEnvironment("local", []string{"127.0.0.1:9092"}, "test")
	if err != nil {
		t.Fatalf("create local client: %v", err)
	}
	client.Close()
}
