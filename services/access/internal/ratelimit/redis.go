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

var allowScript = goredis.NewScript(`
local ip_current = tonumber(redis.call('GET', KEYS[1]) or '0')
local token_current = tonumber(redis.call('GET', KEYS[2]) or '0')
local ip_limit = tonumber(ARGV[1])
local token_limit = tonumber(ARGV[2])
local window_ms = tonumber(ARGV[3])

if ip_current >= ip_limit or token_current >= token_limit then
  return 0
end

ip_current = redis.call('INCR', KEYS[1])
token_current = redis.call('INCR', KEYS[2])
if ip_current == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], window_ms)
end
if token_current == 1 or redis.call('PTTL', KEYS[2]) < 0 then
  redis.call('PEXPIRE', KEYS[2], window_ms)
end
return 1
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
	allowed, err := allowScript.Run(ctx, l.client, []string{ipKey, tokenKey}, l.ipLimit, l.tokenLimit, l.window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("apply public subscription rate limit: %w", err)
	}
	return allowed == 1, nil
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	return "unknown"
}
