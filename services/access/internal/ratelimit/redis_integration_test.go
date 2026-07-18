package ratelimit

import (
	"bytes"
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ZheglY/vpn-platform/services/access/internal/credential"
)

func TestIntegrationRedisRateLimiterIsBoundedAndExpiring(t *testing.T) {
	addr := os.Getenv("ACCESS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("ACCESS_TEST_REDIS_ADDR is not set")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("ACCESS_TEST_REDIS_PASSWORD"), DB: 15})
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hasher, err := credential.NewTokenHasher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := New(client, hasher, 2, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		allowed, err := limiter.Allow(ctx, "192.0.2.10:1234", "token")
		if err != nil {
			t.Fatal(err)
		}
		if allowed != (attempt <= 2) {
			t.Fatalf("attempt %d allowed = %t", attempt, allowed)
		}
	}
	keys, err := client.Keys(ctx, "access:subscription-rate:*").Result()
	if err != nil || len(keys) != 2 {
		t.Fatalf("ephemeral keys = %v, %v", keys, err)
	}
	for _, key := range keys {
		if ttl := client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("key TTL = %s", ttl)
		}
	}
}

func TestIntegrationRedisRateLimiterDoesNotConsumeTheOtherDimension(t *testing.T) {
	client, hasher := integrationRedis(t)
	ctx := context.Background()
	limiter, err := New(client, hasher, 1, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assertAllow(t, limiter, "192.0.2.1:1", "token-a", true)
	assertAllow(t, limiter, "192.0.2.1:2", "token-b", false)
	assertAllow(t, limiter, "192.0.2.2:1", "token-b", true)

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	assertAllow(t, limiter, "192.0.2.1:1", "token-a", true)
	assertAllow(t, limiter, "192.0.2.2:1", "token-a", false)
	assertAllow(t, limiter, "192.0.2.2:2", "token-b", true)
}

func TestIntegrationRedisRateLimiterIsAtomicUnderConcurrency(t *testing.T) {
	client, hasher := integrationRedis(t)
	limiter, err := New(client, hasher, 10, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := limiter.Allow(context.Background(), "192.0.2.10:443", "shared-token")
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed requests = %d, want 10", got)
	}
	keys, err := client.Keys(context.Background(), "access:subscription-rate:*").Result()
	if err != nil || len(keys) != 2 {
		t.Fatalf("rate keys = %v, %v", keys, err)
	}
	for _, key := range keys {
		if count := client.Get(context.Background(), key).Val(); count != "10" {
			t.Fatalf("counter %s = %s", key, count)
		}
	}
}

func integrationRedis(t *testing.T) (*goredis.Client, *credential.TokenHasher) {
	t.Helper()
	addr := os.Getenv("ACCESS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("ACCESS_TEST_REDIS_ADDR is not set")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, Password: os.Getenv("ACCESS_TEST_REDIS_PASSWORD"), DB: 15})
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})
	hasher, err := credential.NewTokenHasher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return client, hasher
}

func assertAllow(t *testing.T, limiter *Limiter, remoteAddr, token string, want bool) {
	t.Helper()
	allowed, err := limiter.Allow(context.Background(), remoteAddr, token)
	if err != nil {
		t.Fatal(err)
	}
	if allowed != want {
		t.Fatalf("Allow(%q, %q) = %t, want %t", remoteAddr, token, allowed, want)
	}
}
