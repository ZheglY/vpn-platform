package ratelimit

import (
	"bytes"
	"context"
	"os"
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
