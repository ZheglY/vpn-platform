package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

type Worker struct {
	service      *Service
	store        domain.Store
	publisher    Publisher
	logger       *zap.Logger
	pollInterval time.Duration
	lease        time.Duration
}

func NewWorker(service *Service, store domain.Store, publisher Publisher, logger *zap.Logger, pollInterval, lease time.Duration) *Worker {
	return &Worker{service: service, store: store, publisher: publisher, logger: logger, pollInterval: pollInterval, lease: lease}
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
	if err := w.processWebhook(ctx); err != nil {
		w.logFailure("billing webhook worker failed", err)
	}
	if err := w.reconcilePayment(ctx); err != nil {
		w.logFailure("billing reconciliation failed", err)
	}
	if err := w.publishOutbox(ctx); err != nil {
		w.logFailure("billing outbox publish failed", err)
	}
}

func (w *Worker) processWebhook(ctx context.Context) error {
	notification, ok, err := w.store.ClaimWebhook(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	operation, err := w.store.GetPaymentByProviderID(ctx, notification.ProviderObjectID)
	if errors.Is(err, domain.ErrNotFound) {
		return w.store.RejectWebhook(ctx, notification.InboxID, "unknown_payment")
	}
	if err != nil {
		return w.store.RetryWebhook(ctx, notification.InboxID, "database_error", retryBackoff(w.service.retryDelay, notification.Attempts, notification.InboxID))
	}
	providerPayment, err := w.service.provider.GetPayment(ctx, notification.ProviderObjectID)
	if err != nil {
		if domain.IsProviderRetryable(err) {
			return w.store.RetryWebhook(ctx, notification.InboxID, providerErrorCode(err), retryBackoff(w.service.retryDelay, notification.Attempts, notification.InboxID))
		}
		return w.store.RejectWebhook(ctx, notification.InboxID, providerErrorCode(err))
	}
	if err := w.service.validateProviderPayment(operation, providerPayment, false); err != nil {
		return w.store.RejectWebhook(ctx, notification.InboxID, "provider_mismatch")
	}
	if err := w.applyProviderState(ctx, operation, providerPayment); err != nil {
		if errors.Is(err, domain.ErrStateConflict) {
			return w.store.RejectWebhook(ctx, notification.InboxID, "state_conflict")
		}
		return w.store.RetryWebhook(ctx, notification.InboxID, "apply_failed", retryBackoff(w.service.retryDelay, notification.Attempts, notification.InboxID))
	}
	return w.store.CompleteWebhook(ctx, notification.InboxID, "verified")
}

func (w *Worker) reconcilePayment(ctx context.Context) error {
	operation, ok, err := w.store.ClaimPaymentForReconcile(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	if operation.ProviderPaymentID == nil {
		if !w.service.now().Before(operation.ProviderCreateDeadline) {
			return w.store.MarkProviderCreateFailed(ctx, operation.PaymentID, "create_window_expired")
		}
		created, err := w.service.createAtProvider(ctx, operation)
		if err != nil {
			return nil
		}
		return w.store.ReschedulePayment(ctx, created.PaymentID, "", retryBackoff(w.service.retryDelay, operation.ReconcileAttempts, operation.PaymentID))
	}
	providerPayment, err := w.service.provider.GetPayment(ctx, *operation.ProviderPaymentID)
	if err != nil {
		if domain.IsProviderRetryable(err) {
			return w.store.ReschedulePayment(ctx, operation.PaymentID, providerErrorCode(err), retryBackoff(w.service.retryDelay, operation.ReconcileAttempts, operation.PaymentID))
		}
		return w.store.MarkProviderCreateFailed(ctx, operation.PaymentID, providerErrorCode(err))
	}
	if err := w.service.validateProviderPayment(operation, providerPayment, false); err != nil {
		return w.store.MarkProviderCreateFailed(ctx, operation.PaymentID, "provider_mismatch")
	}
	return w.applyProviderState(ctx, operation, providerPayment)
}

func (w *Worker) applyProviderState(ctx context.Context, operation domain.PaymentOperation, providerPayment domain.ProviderPayment) error {
	if providerPayment.Status == "pending" {
		return w.store.ReschedulePayment(ctx, operation.PaymentID, "", time.Minute)
	}
	now := w.service.now()
	if providerPayment.Status == "succeeded" && providerPayment.CapturedAt == nil {
		return fmt.Errorf("succeeded provider payment has no captured_at")
	}
	if providerPayment.Status == "canceled" && providerPayment.CanceledAt == nil {
		providerPayment.CanceledAt = &now
	}
	correlationID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	return w.store.ApplyVerifiedPayment(ctx, operation, providerPayment, correlationID)
}

func (w *Worker) publishOutbox(ctx context.Context) error {
	message, ok, err := w.store.ClaimOutbox(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	if err := w.publisher.Publish(publishCtx, message.Topic, message.PartitionKey, message.Payload); err != nil {
		delay := retryBackoff(w.service.retryDelay, message.Attempts, message.EventID)
		return w.store.RetryOutbox(ctx, message.EventID, delay)
	}
	return w.store.CompleteOutbox(ctx, message.EventID)
}

func retryBackoff(base time.Duration, attempt int, key string) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", key, attempt)))
	percent := 80 + int(sum[0])%41
	delay = delay * time.Duration(percent) / 100
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (w *Worker) logFailure(message string, err error) {
	w.logger.Warn(message, zap.String("error_type", fmt.Sprintf("%T", err)))
}
