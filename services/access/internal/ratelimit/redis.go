package ratelimit

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ZheglY/vpn-platform/services/access/internal/credential"
)

var incrementScript = goredis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return current
`)

type Limiter struct {
	client     *goredis.Client
	hasher     *credential.TokenHasher
	ipLimit    int64
	tokenLimit int64
	window     time.Duration
	prefix     string
}

func New(client *goredis.Client, hasher *credential.TokenHasher, ipLimit, tokenLimit int64, window time.Duration) (*Limiter, error) {
	if client == nil || hasher == nil {
		return nil, fmt.Errorf("redis client and keyed hasher are required")
	}
	if ipLimit <= 0 || tokenLimit <= 0 || window <= 0 {
		return nil, fmt.Errorf("rate limits and window must be positive")
	}
	return &Limiter{client: client, hasher: hasher, ipLimit: ipLimit, tokenLimit: tokenLimit, window: window, prefix: "access:subscription-rate:"}, nil
}

func (l *Limiter) Allow(ctx context.Context, remoteAddr, token string) (bool, error) {
	ip := remoteIP(remoteAddr)
	ipKey := l.prefix + "ip:" + fmt.Sprintf("%x", l.hasher.Sum("rate-ip\x00"+ip))
	tokenKey := l.prefix + "token:" + fmt.Sprintf("%x", l.hasher.Sum("rate-token\x00"+token))
	ipAllowed, err := l.increment(ctx, ipKey, l.ipLimit)
	if err != nil {
		return false, err
	}
	tokenAllowed, err := l.increment(ctx, tokenKey, l.tokenLimit)
	if err != nil {
		return false, err
	}
	return ipAllowed && tokenAllowed, nil
}

func (l *Limiter) increment(ctx context.Context, key string, limit int64) (bool, error) {
	count, err := incrementScript.Run(ctx, l.client, []string{key}, l.window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("apply public subscription rate limit: %w", err)
	}
	return count <= limit, nil
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	return "unknown"
}
