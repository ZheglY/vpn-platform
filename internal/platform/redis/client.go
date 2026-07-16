package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

func NewClient(addr, password string, db int) *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
}

func Ping(ctx context.Context, client *goredis.Client) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}
