package redisstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type Store struct {
	client        *goredis.Client
	dedupeTTL     time.Duration
	processingTTL time.Duration
	fsmTTL        time.Duration
	keyPrefix     string
}

func New(client *goredis.Client, keyPrefix string, dedupeTTL, processingTTL, fsmTTL time.Duration) *Store {
	if keyPrefix == "" {
		keyPrefix = "telegram"
	}
	if processingTTL <= 0 {
		processingTTL = 30 * time.Second
	}
	return &Store{
		client:        client,
		dedupeTTL:     dedupeTTL,
		processingTTL: processingTTL,
		fsmTTL:        fsmTTL,
		keyPrefix:     keyPrefix,
	}
}

func (s *Store) StartProcessing(ctx context.Context, updateID int64, token string) (string, error) {
	keys := []string{s.completedKey(updateID), s.processingKey(updateID)}
	result, err := s.client.Eval(ctx, `
if redis.call("EXISTS", KEYS[1]) == 1 then
  return "completed"
end
if redis.call("SET", KEYS[2], ARGV[1], "NX", "PX", ARGV[2]) then
  return "acquired"
end
return "processing"
`, keys, token, ttlMillis(s.processingTTL)).Text()
	if err != nil {
		return "", fmt.Errorf("start telegram update processing: %w", err)
	}
	return result, nil
}

func (s *Store) CompleteProcessing(ctx context.Context, updateID int64, token string) error {
	keys := []string{s.completedKey(updateID), s.processingKey(updateID)}
	if err := s.client.Eval(ctx, `
redis.call("SET", KEYS[1], "1", "PX", ARGV[2])
if redis.call("GET", KEYS[2]) == ARGV[1] then
  redis.call("DEL", KEYS[2])
end
return 1
`, keys, token, ttlMillis(s.dedupeTTL)).Err(); err != nil {
		return fmt.Errorf("complete telegram update processing: %w", err)
	}
	return nil
}

func (s *Store) ReleaseProcessing(ctx context.Context, updateID int64, token string) error {
	if err := s.client.Eval(ctx, `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`, []string{s.processingKey(updateID)}, token).Err(); err != nil {
		return fmt.Errorf("release telegram update processing: %w", err)
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

func (s *Store) StartDelivery(ctx context.Context, deliveryID, requestHash, token string) (string, error) {
	keys := []string{s.deliveryCompletedKey(deliveryID), s.deliveryProcessingKey(deliveryID)}
	result, err := s.client.Eval(ctx, `
local completed = redis.call("GET", KEYS[1])
if completed then
  if completed == ARGV[1] then return "completed" end
  return "conflict"
end
local processing = redis.call("GET", KEYS[2])
if processing then
  if string.sub(processing, 1, 64) == ARGV[1] then return "processing" end
  return "conflict"
end
redis.call("SET", KEYS[2], ARGV[1] .. "|" .. ARGV[2], "PX", ARGV[3])
return "acquired"
`, keys, requestHash, token, ttlMillis(s.processingTTL)).Text()
	if err != nil {
		return "", fmt.Errorf("start Telegram delivery: %w", err)
	}
	return result, nil
}

func (s *Store) CompleteDelivery(ctx context.Context, deliveryID, requestHash, token string) error {
	keys := []string{s.deliveryCompletedKey(deliveryID), s.deliveryProcessingKey(deliveryID)}
	result, err := s.client.Eval(ctx, `
if redis.call("GET", KEYS[2]) ~= ARGV[1] .. "|" .. ARGV[2] then return 0 end
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[3])
redis.call("DEL", KEYS[2])
return 1
`, keys, requestHash, token, ttlMillis(s.dedupeTTL)).Int()
	if err != nil {
		return fmt.Errorf("complete Telegram delivery: %w", err)
	}
	if result != 1 {
		return fmt.Errorf("telegram delivery lease was lost")
	}
	return nil
}

func (s *Store) ReleaseDelivery(ctx context.Context, deliveryID, requestHash, token string) error {
	result, err := s.client.Eval(ctx, `
if redis.call("GET", KEYS[1]) == ARGV[1] .. "|" .. ARGV[2] then
  return redis.call("DEL", KEYS[1])
end
return 0
`, []string{s.deliveryProcessingKey(deliveryID)}, requestHash, token).Int()
	if err != nil {
		return fmt.Errorf("release Telegram delivery: %w", err)
	}
	if result != 1 {
		return fmt.Errorf("telegram delivery lease was lost")
	}
	return nil
}

func (s *Store) completedKey(updateID int64) string {
	return s.keyPrefix + ":dedupe:completed:" + strconv.FormatInt(updateID, 10)
}

func (s *Store) processingKey(updateID int64) string {
	return s.keyPrefix + ":dedupe:processing:" + strconv.FormatInt(updateID, 10)
}

func (s *Store) deliveryCompletedKey(deliveryID string) string {
	return s.keyPrefix + ":delivery:completed:" + deliveryID
}

func (s *Store) deliveryProcessingKey(deliveryID string) string {
	return s.keyPrefix + ":delivery:processing:" + deliveryID
}

func ttlMillis(ttl time.Duration) int64 {
	if ttl <= 0 {
		ttl = time.Second
	}
	return ttl.Milliseconds()
}
