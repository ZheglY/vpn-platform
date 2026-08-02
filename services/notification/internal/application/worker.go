package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
	templatex "github.com/ZheglY/vpn-platform/services/notification/internal/template"
)

type Worker struct {
	store        domain.Store
	identity     domain.IdentityClient
	subscription domain.SubscriptionClient
	access       domain.AccessClient
	telegram     domain.TelegramClient
	logger       *zap.Logger
	pollInterval time.Duration
	retryBase    time.Duration
	lease        time.Duration
}

func NewWorker(store domain.Store, identity domain.IdentityClient, subscription domain.SubscriptionClient, access domain.AccessClient, telegram domain.TelegramClient, logger *zap.Logger, poll, retryBase, lease time.Duration) *Worker {
	return &Worker{store: store, identity: identity, subscription: subscription, access: access, telegram: telegram, logger: logger, pollInterval: poll, retryBase: retryBase, lease: lease}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.workOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("notification delivery attempt failed", zap.String("error_type", fmt.Sprintf("%T", err)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) workOnce(ctx context.Context) error {
	job, ok, err := w.store.ClaimJob(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	target, err := w.identity.GetNotificationTarget(ctx, job.UserID)
	if err != nil {
		return w.retryOrFail(ctx, job, &domain.DeliveryError{Code: "identity_unavailable", Retryable: true})
	}
	if !target.Eligible {
		reason := target.ReasonCode
		if reason == "" {
			reason = "target_unavailable"
		}
		return w.store.SuppressJob(ctx, job.NotificationID, job.ClaimID, reason)
	}
	if isSubscriptionStateSensitive(job.NotificationType) && job.SubscriptionID != nil {
		state, err := w.subscription.GetState(ctx, job.UserID, *job.SubscriptionID)
		if err != nil {
			return w.retryOrFail(ctx, job, &domain.DeliveryError{Code: "subscription_state_unavailable", Retryable: true})
		}
		if !subscriptionNotificationCurrent(job, state) {
			return w.store.SuppressJob(ctx, job.NotificationID, job.ClaimID, "stale_subscription_state")
		}
	}
	if (job.NotificationType == "access_ready" || job.NotificationType == "access_degraded") && job.SubscriptionID != nil && job.CredentialID != nil {
		current, err := w.access.IsCurrentReady(ctx, *job.SubscriptionID, *job.CredentialID)
		if err != nil {
			return w.retryOrFail(ctx, job, &domain.DeliveryError{Code: "access_state_unavailable", Retryable: true})
		}
		if !current {
			return w.store.SuppressJob(ctx, job.NotificationID, job.ClaimID, "stale_access_state")
		}
	}
	text, err := templatex.Render(job.NotificationType, job.TemplateVersion, job.Variables)
	if err != nil {
		return w.store.FailJob(ctx, job.NotificationID, job.ClaimID, "invalid_template")
	}
	_, err = w.telegram.Deliver(ctx, job.NotificationID, target.TelegramChatID, job.NotificationType, job.TemplateVersion, text)
	if err != nil {
		return w.retryOrFail(ctx, job, err)
	}
	return w.store.CompleteJob(ctx, job.NotificationID, job.ClaimID)
}

func isSubscriptionStateSensitive(notificationType string) bool {
	switch notificationType {
	case "subscription_extended", "subscription_grace", "subscription_expired", "subscription_revoked",
		"access_ready", "access_degraded", "provisioning_failed":
		return true
	default:
		return false
	}
}

func subscriptionNotificationCurrent(job domain.Job, state domain.SubscriptionState) bool {
	switch job.NotificationType {
	case "subscription_extended":
		expected, err := time.Parse(time.RFC3339Nano, job.Variables["period_end"])
		return err == nil && state.Status == "active" && state.CurrentPeriodEnd != nil && state.CurrentPeriodEnd.Equal(expected)
	case "subscription_grace":
		expected, err := time.Parse(time.RFC3339Nano, job.Variables["grace_ends_at"])
		return err == nil && state.Status == "grace" && state.GraceEndsAt != nil && state.GraceEndsAt.Equal(expected)
	case "subscription_expired":
		return state.Status == "expired"
	case "subscription_revoked":
		return state.Status == "revoked"
	case "access_ready", "access_degraded", "provisioning_failed":
		return state.Status == "active" || state.Status == "grace"
	default:
		return true
	}
}

func (w *Worker) retryOrFail(ctx context.Context, job domain.Job, err error) error {
	var deliveryErr *domain.DeliveryError
	if !errors.As(err, &deliveryErr) {
		deliveryErr = &domain.DeliveryError{Code: "unknown_delivery_error", Retryable: true}
	}
	if !deliveryErr.Retryable || job.Attempts >= job.MaxAttempts {
		return w.store.FailJob(ctx, job.NotificationID, job.ClaimID, deliveryErr.Code)
	}
	delay := deliveryErr.RetryAfter
	if delay <= 0 {
		delay = retryBackoff(w.retryBase, job.Attempts, job.NotificationID)
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	return w.store.RetryJob(ctx, job.NotificationID, job.ClaimID, delay)
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
