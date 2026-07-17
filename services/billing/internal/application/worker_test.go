package application

import (
	"testing"
	"time"
)

func TestRetryBackoffIsDeterministicGrowingAndCapped(t *testing.T) {
	first := retryBackoff(time.Second, 1, "operation-a")
	if again := retryBackoff(time.Second, 1, "operation-a"); again != first {
		t.Fatalf("retry backoff changed: first=%s again=%s", first, again)
	}
	second := retryBackoff(time.Second, 2, "operation-a")
	if first < 800*time.Millisecond || first > 1200*time.Millisecond || second <= first {
		t.Fatalf("unexpected jittered backoff: first=%s second=%s", first, second)
	}
	if capped := retryBackoff(time.Hour, 20, "operation-a"); capped > 5*time.Minute {
		t.Fatalf("retry backoff exceeded cap: %s", capped)
	}
}
