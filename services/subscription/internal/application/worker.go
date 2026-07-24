package application

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
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
	metrics      platformkafka.Observer
}

func NewWorker(store domain.Store, publisher Publisher, logger *zap.Logger, pollInterval, retryDelay, lease time.Duration, observers ...platformkafka.Observer) *Worker {
	return &Worker{store: store, publisher: publisher, logger: logger, pollInterval: pollInterval, retryDelay: retryDelay, lease: lease, metrics: platformkafka.ObserverOrNoop(observers...)}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		w.workOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) workOnce(ctx context.Context) {
	if err := w.reconcileRefund(ctx); err != nil {
		w.logFailure("subscription refund reconciliation failed", err)
	}
	if err := w.advanceLifecycle(ctx); err != nil {
		w.logFailure("subscription lifecycle transition failed", err)
	}
	if err := w.publishOutbox(ctx); err != nil {
		w.logFailure("subscription outbox publish failed", err)
	}
}

func (w *Worker) reconcileRefund(ctx context.Context) error {
	work, ok, err := w.store.ClaimRefund(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	delay := RetryBackoff(w.retryDelay, work.Attempts, work.InboxID)
	return w.store.ApplyClaimedRefund(ctx, work, delay)
}

func (w *Worker) advanceLifecycle(ctx context.Context) error {
	subscription, ok, err := w.store.ClaimDue(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	return w.store.CompleteDue(ctx, subscription.SubscriptionID)
}

func (w *Worker) publishOutbox(ctx context.Context) error {
	message, ok, err := w.store.ClaimOutbox(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	startedAt := time.Now()
	if err := w.publisher.Publish(publishCtx, message.Topic, message.PartitionKey, message.Payload); err != nil {
		w.metrics.ObserveOutbox(message.Topic, "retry", startedAt)
		w.metrics.ObserveRetry(message.Topic, "outbox")
		return w.store.RetryOutbox(ctx, message.EventID, RetryBackoff(w.retryDelay, message.Attempts, message.EventID))
	}
	w.metrics.ObserveOutbox(message.Topic, "success", startedAt)
	return w.store.CompleteOutbox(ctx, message.EventID)
}

func (w *Worker) logFailure(message string, err error) {
	w.logger.Warn(message, zap.String("error_type", classifyWorkerError(err)), zap.String("go_error_type", fmt.Sprintf("%T", err)))
}
