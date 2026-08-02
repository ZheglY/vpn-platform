package redis

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ZheglY/vpn-platform/internal/platform/tlsconfig"
)

func NewClient(addr, password string, db int) *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
}

func NewClientForEnvironment(environment, addr, password string, db int) (*goredis.Client, error) {
	if environment != "staging" && environment != "production" {
		return NewClient(addr, password, db), nil
	}
	caFile := strings.TrimSpace(os.Getenv("REDIS_TLS_CA_FILE"))
	if caFile == "" {
		return nil, fmt.Errorf("REDIS_TLS_CA_FILE is required")
	}
	tlsConfig, err := tlsconfig.NewClient([]string{caFile}, "", "")
	if err != nil {
		return nil, fmt.Errorf("configure Redis TLS: %w", err)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("REDIS_ADDR must be host:port")
	}
	tlsConfig.ServerName = strings.Trim(host, "[]")
	return goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: db, TLSConfig: tlsConfig}), nil
}

func Ping(ctx context.Context, client *goredis.Client) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}
