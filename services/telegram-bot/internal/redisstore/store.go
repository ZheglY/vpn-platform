package redisstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type Store struct {
	client    *goredis.Client
	dedupeTTL time.Duration
	fsmTTL    time.Duration
	keyPrefix string
}

func New(client *goredis.Client, keyPrefix string, dedupeTTL, fsmTTL time.Duration) *Store {
	if keyPrefix == "" {
		keyPrefix = "telegram"
	}
	return &Store{
		client:    client,
		dedupeTTL: dedupeTTL,
		fsmTTL:    fsmTTL,
		keyPrefix: keyPrefix,
	}
}

func (s *Store) MarkProcessed(ctx context.Context, updateID int64) (bool, error) {
	key := s.keyPrefix + ":dedupe:" + strconv.FormatInt(updateID, 10)
	ok, err := s.client.SetNX(ctx, key, "1", s.dedupeTTL).Result()
	if err != nil {
		return false, fmt.Errorf("set telegram update dedupe key: %w", err)
	}
	return ok, nil
}

func (s *Store) ForgetProcessed(ctx context.Context, updateID int64) error {
	key := s.keyPrefix + ":dedupe:" + strconv.FormatInt(updateID, 10)
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("delete telegram update dedupe key: %w", err)
	}
	return nil
}

func (s *Store) GetState(ctx context.Context, telegramUserID int64) (string, error) {
	key := s.keyPrefix + ":fsm:" + strconv.FormatInt(telegramUserID, 10)
	value, err := s.client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get telegram FSM state: %w", err)
	}
	return value, nil
}

func (s *Store) SetState(ctx context.Context, telegramUserID int64, state string) error {
	key := s.keyPrefix + ":fsm:" + strconv.FormatInt(telegramUserID, 10)
	if err := s.client.Set(ctx, key, state, s.fsmTTL).Err(); err != nil {
		return fmt.Errorf("set telegram FSM state: %w", err)
	}
	return nil
}
