package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

type ContractError struct {
	Code string
	Err  error
}

func (e *ContractError) Error() string { return e.Code }
func (e *ContractError) Unwrap() error { return e.Err }

func ContractErrorCode(err error) (string, bool) {
	var contractErr *ContractError
	if errors.As(err, &contractErr) {
		return contractErr.Code, true
	}
	if errors.Is(err, domain.ErrEventConflict) {
		return "durable_event_conflict", true
	}
	return "", false
}

type Service struct {
	store       domain.Store
	maxAttempts int
}

func NewService(store domain.Store, maxAttempts int) *Service {
	if maxAttempts < 1 {
		maxAttempts = 8
	}
	return &Service{store: store, maxAttempts: maxAttempts}
}

func (s *Service) ProcessEvent(ctx context.Context, meta domain.EventMeta, data json.RawMessage) error {
	intent, err := s.intent(meta, data)
	if err != nil {
		return err
	}
	_, err = s.store.RecordEvent(ctx, meta, intent)
	return err
}

func (s *Service) intent(meta domain.EventMeta, data json.RawMessage) (domain.Intent, error) {
	notificationID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Intent{}, err
	}
	base := domain.Intent{NotificationID: notificationID, TemplateVersion: 1, MaxAttempts: s.maxAttempts, Variables: map[string]string{}}
	switch meta.EventType {
	case "billing.payment.succeeded.v1":
		var event paymentSucceeded
		if err := strictDecode(data, &event); err != nil || event.PaymentID != meta.AggregateID || event.UserID == "" || event.PaidAt.IsZero() || event.AmountMinor < 1 {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.NotificationType = event.UserID, "payment_confirmed"
		base.BusinessDedupeKey = "payment_confirmed:" + event.PaymentID
		base.DeliveryStreamKey, base.DeliverySequence = "payment:"+event.PaymentID, 1
		base.Variables = map[string]string{
			"amount_minor": strconv.FormatInt(event.AmountMinor, 10),
			"currency":     event.Currency,
		}
	case "billing.refund.succeeded.v1":
		var event refundSucceeded
		if err := strictDecode(data, &event); err != nil || event.RefundID != meta.AggregateID || event.PaymentID == "" || event.UserID == "" || event.RefundScope != "full" || event.RefundedAt.IsZero() {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.NotificationType = event.UserID, "refund_confirmed"
		base.BusinessDedupeKey = "refund_confirmed:" + event.RefundID
		base.DeliveryStreamKey, base.DeliverySequence, base.SupersedesOlder = "payment:"+event.PaymentID, 2, true
	case "subscription.activated.v1", "subscription.extended.v1":
		var event periodEvent
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.SubscriptionID != meta.AggregateID || event.UserID == "" || event.PeriodID == "" || event.PeriodEnd.IsZero() {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID = event.UserID, &event.SubscriptionID
		base.DeliveryStreamKey, base.DeliverySequence = "subscription:"+event.SubscriptionID, meta.AggregateSequence
		base.Variables = map[string]string{"period_end": event.PeriodEnd.UTC().Format(time.RFC3339Nano)}
		if meta.EventType == "subscription.activated.v1" {
			base.NotificationType = "subscription_activated"
			base.BusinessDedupeKey = "activation:" + event.PeriodID
			base.SuppressedReason = "covered_by_payment_and_ready"
		} else {
			base.NotificationType = "subscription_extended"
			base.BusinessDedupeKey = "extension:" + event.PeriodID
		}
	case "subscription.grace.started.v1":
		var event graceStarted
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.SubscriptionID != meta.AggregateID || event.UserID == "" || event.GraceEndsAt.IsZero() || !event.GraceEndsAt.After(event.PeriodEnd) {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID, base.NotificationType = event.UserID, &event.SubscriptionID, "subscription_grace"
		base.BusinessDedupeKey = fmt.Sprintf("grace:%s:%d", event.SubscriptionID, meta.AggregateSequence)
		base.DeliveryStreamKey, base.DeliverySequence = "subscription:"+event.SubscriptionID, meta.AggregateSequence
		base.Variables = map[string]string{"grace_ends_at": event.GraceEndsAt.UTC().Format(time.RFC3339Nano)}
	case "subscription.expired.v1", "subscription.revoked.v1":
		var event terminalEvent
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.SubscriptionID != meta.AggregateID || event.UserID == "" || event.EffectiveAt.IsZero() {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID = event.UserID, &event.SubscriptionID
		base.DeliveryStreamKey, base.DeliverySequence, base.SupersedesOlder = "subscription:"+event.SubscriptionID, meta.AggregateSequence, true
		base.NotificationType = "subscription_expired"
		if meta.EventType == "subscription.revoked.v1" {
			base.NotificationType = "subscription_revoked"
		}
		base.BusinessDedupeKey = fmt.Sprintf("terminal:%s:%d", event.SubscriptionID, meta.AggregateSequence)
		base.Variables = map[string]string{"reason": event.Reason}
	case "access.ready.v1":
		var event accessReady
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.CredentialID != meta.AggregateID || event.UserID == "" || event.SubscriptionID == "" || event.ReadyAt.IsZero() || !event.LinkIssuanceRequired || (event.ProvisioningStatus != "active" && event.ProvisioningStatus != "degraded") {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID, base.CredentialID = event.UserID, &event.SubscriptionID, &event.CredentialID
		base.DeliveryStreamKey, base.DeliverySequence = "access:"+event.CredentialID, meta.AggregateSequence
		base.NotificationType = "access_ready"
		if event.ProvisioningStatus == "degraded" {
			base.NotificationType = "access_degraded"
		}
		base.BusinessDedupeKey = fmt.Sprintf("access_ready:%s:%d", event.CredentialID, meta.AggregateSequence)
	case "access.provisioning.failed.v1":
		var event provisioningFailed
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.CredentialID != meta.AggregateID || event.UserID == "" || event.SubscriptionID == "" || event.FailedRevision < 1 || event.FailedAt.IsZero() {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID, base.CredentialID = event.UserID, &event.SubscriptionID, &event.CredentialID
		base.DeliveryStreamKey, base.DeliverySequence = "access:"+event.CredentialID, meta.AggregateSequence
		base.NotificationType = "provisioning_failed"
		base.BusinessDedupeKey = fmt.Sprintf("provisioning_failed:%s:%d", event.CredentialID, event.FailedRevision)
	case "access.revoked.v1":
		var event accessRevoked
		if err := strictDecode(data, &event); err != nil || meta.AggregateSequence < 1 || event.CredentialID != meta.AggregateID || event.UserID == "" || event.SubscriptionID == "" || event.DesiredRevision < 1 || event.RevokedAt.IsZero() {
			return domain.Intent{}, contract("invalid_event_data", err)
		}
		base.UserID, base.SubscriptionID, base.CredentialID = event.UserID, &event.SubscriptionID, &event.CredentialID
		base.DeliveryStreamKey, base.DeliverySequence, base.SupersedesOlder = "access:"+event.CredentialID, meta.AggregateSequence, true
		base.NotificationType = "access_physically_revoked"
		base.BusinessDedupeKey = fmt.Sprintf("access_revoked:%s:%d", event.CredentialID, event.DesiredRevision)
		base.SuppressedReason = "covered_by_entitlement_terminal"
	default:
		return domain.Intent{}, contract("unsupported_event_type", nil)
	}
	if meta.PartitionKey != "user:"+base.UserID {
		return domain.Intent{}, contract("event_partition_mismatch", nil)
	}
	if base.DeliveryStreamKey == "" || base.DeliverySequence < 1 {
		return domain.Intent{}, contract("invalid_delivery_order", nil)
	}
	return base, nil
}

func contract(code string, err error) error {
	if err == nil {
		err = domain.ErrEventConflict
	}
	return &ContractError{Code: code, Err: err}
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

type paymentSucceeded struct {
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	UserID      string    `json:"user_id"`
	PlanID      string    `json:"plan_id"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	PaidAt      time.Time `json:"paid_at"`
}

type refundSucceeded struct {
	RefundID    string    `json:"refund_id"`
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	UserID      string    `json:"user_id"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	RefundScope string    `json:"refund_scope"`
	RefundedAt  time.Time `json:"refunded_at"`
}

type periodEvent struct {
	SubscriptionID  string    `json:"subscription_id"`
	UserID          string    `json:"user_id"`
	PeriodID        string    `json:"period_id"`
	SourceOrderID   string    `json:"source_order_id"`
	SourcePaymentID string    `json:"source_payment_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	GraceEndsAt     time.Time `json:"grace_ends_at"`
}

type graceStarted struct {
	SubscriptionID string    `json:"subscription_id"`
	UserID         string    `json:"user_id"`
	PeriodEnd      time.Time `json:"period_end"`
	GraceEndsAt    time.Time `json:"grace_ends_at"`
	EffectiveAt    time.Time `json:"effective_at"`
}

type terminalEvent struct {
	SubscriptionID    string    `json:"subscription_id"`
	UserID            string    `json:"user_id"`
	Reason            string    `json:"reason"`
	AffectedPeriodIDs []string  `json:"affected_period_ids,omitempty"`
	EffectiveAt       time.Time `json:"effective_at"`
}

type accessReady struct {
	SubscriptionID       string    `json:"subscription_id"`
	CredentialID         string    `json:"credential_id"`
	UserID               string    `json:"user_id"`
	ProvisioningStatus   string    `json:"provisioning_status"`
	ReadyAt              time.Time `json:"ready_at"`
	LinkIssuanceRequired bool      `json:"link_issuance_required"`
}

type provisioningFailed struct {
	SubscriptionID string    `json:"subscription_id"`
	CredentialID   string    `json:"credential_id"`
	UserID         string    `json:"user_id"`
	FailedRevision int       `json:"failed_revision"`
	ReasonCode     string    `json:"reason_code"`
	FailedAt       time.Time `json:"failed_at"`
}

type accessRevoked struct {
	SubscriptionID  string    `json:"subscription_id"`
	CredentialID    string    `json:"credential_id"`
	UserID          string    `json:"user_id"`
	DesiredRevision int       `json:"desired_revision"`
	Reason          string    `json:"reason"`
	RevokedAt       time.Time `json:"revoked_at"`
}
