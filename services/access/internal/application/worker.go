package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

type Worker struct {
	store        domain.Store
	publisher    Publisher
	logger       *zap.Logger
	pollInterval time.Duration
	retryDelay   time.Duration
	lease        time.Duration
}

func NewWorker(store domain.Store, publisher Publisher, logger *zap.Logger, pollInterval, retryDelay, lease time.Duration) *Worker {
	return &Worker{store: store, publisher: publisher, logger: logger, pollInterval: pollInterval, retryDelay: retryDelay, lease: lease}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.workOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("access outbox publish failed", zap.String("error_type", fmt.Sprintf("%T", err)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) workOnce(ctx context.Context) error {
	message, ok, err := w.store.ClaimOutbox(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	if err := w.publisher.Publish(publishCtx, message.Topic, message.PartitionKey, message.Payload); err != nil {
		return w.store.RetryOutbox(ctx, message.EventID, retryBackoff(w.retryDelay, message.Attempts, message.EventID))
	}
	return w.store.CompleteOutbox(ctx, message.EventID)
}

func retryBackoff(base time.Duration, attempts int, key string) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	exponent := attempts - 1
	if exponent > 8 {
		exponent = 8
	}
	delay := base * time.Duration(1<<exponent)
	sum := sha256.Sum256([]byte(key))
	jitter := time.Duration(binary.BigEndian.Uint16(sum[:2])) * delay / 655350
	return delay + jitter
}
