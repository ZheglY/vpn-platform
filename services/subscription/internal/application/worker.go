package application

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

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
	now          func() time.Time
}

func NewWorker(store domain.Store, publisher Publisher, logger *zap.Logger, pollInterval, retryDelay, lease time.Duration) *Worker {
	return &Worker{store: store, publisher: publisher, logger: logger, pollInterval: pollInterval, retryDelay: retryDelay, lease: lease, now: func() time.Time { return time.Now().UTC() }}
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
	return w.store.ApplyClaimedRefund(ctx, work, w.now(), delay)
}

func (w *Worker) advanceLifecycle(ctx context.Context) error {
	subscription, ok, err := w.store.ClaimDue(ctx, w.now(), w.lease)
	if err != nil || !ok {
		return err
	}
	return w.store.CompleteDue(ctx, subscription.SubscriptionID, w.now())
}

func (w *Worker) publishOutbox(ctx context.Context) error {
	message, ok, err := w.store.ClaimOutbox(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	if err := w.publisher.Publish(publishCtx, message.Topic, message.PartitionKey, message.Payload); err != nil {
		return w.store.RetryOutbox(ctx, message.EventID, RetryBackoff(w.retryDelay, message.Attempts, message.EventID))
	}
	return w.store.CompleteOutbox(ctx, message.EventID)
}

func (w *Worker) logFailure(message string, err error) {
	w.logger.Warn(message, zap.String("error_type", classifyWorkerError(err)), zap.String("go_error_type", fmt.Sprintf("%T", err)))
}
