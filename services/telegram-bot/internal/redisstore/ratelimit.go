package redisstore

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	client    *goredis.Client
	keyPrefix string
	limit     int64
	window    time.Duration
}

func NewRateLimiter(client *goredis.Client, keyPrefix string, limit int64, window time.Duration) *RateLimiter {
	if keyPrefix == "" {
		keyPrefix = "telegram"
	}
	if limit <= 0 {
		limit = 60
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		client:    client,
		keyPrefix: keyPrefix,
		limit:     limit,
		window:    window,
	}
}

func (l *RateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := l.keyPrefix + ":rate:" + key
	count, err := l.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("increment rate limit counter: %w", err)
	}
	if count == 1 {
		if err := l.client.Expire(ctx, redisKey, l.window).Err(); err != nil {
			return false, fmt.Errorf("expire rate limit counter: %w", err)
		}
	}
	return count <= l.limit, nil
}
