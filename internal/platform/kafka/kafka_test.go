package kafka

import "testing"

func TestNewClientRequiresBroker(t *testing.T) {
	if _, err := NewClient(nil, "test"); err == nil {
		t.Fatal("expected error for missing brokers")
	}
}
