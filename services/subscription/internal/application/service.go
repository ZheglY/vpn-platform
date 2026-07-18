package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type ContractError struct{ Code string }

func (e *ContractError) Error() string { return "subscription event contract violation: " + e.Code }

func ContractErrorCode(err error) (string, bool) {
	var contractErr *ContractError
	if errors.As(err, &contractErr) {
		return contractErr.Code, true
	}
	if errors.Is(err, domain.ErrConflict) {
		return "durable_state_conflict", true
	}
	return "", false
}

type Service struct {
	store   domain.Store
	billing domain.Billing
	now     func() time.Time
}

func NewService(store domain.Store, billing domain.Billing) *Service {
	return &Service{store: store, billing: billing, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) HandlePayment(ctx context.Context, meta domain.EventMeta, payment domain.PaymentSucceeded) error {
	if err := validatePayment(meta, payment); err != nil {
		return err
	}
	recorded, err := s.store.RecordPaymentReplay(ctx, meta, payment, s.now())
	if err != nil || recorded {
		return err
	}
	order, err := s.billing.GetOrder(ctx, payment.UserID, payment.OrderID)
	if err != nil {
		return err
	}
	if err := validateOrder(payment, order); err != nil {
		return err
	}
	return s.store.ApplyPayment(ctx, meta, payment, order, s.now())
}

func (s *Service) HandleRefund(ctx context.Context, meta domain.EventMeta, refund domain.RefundSucceeded) error {
	if err := validateRefund(meta, refund); err != nil {
		return err
	}
	return s.store.StoreRefund(ctx, meta, refund, s.now())
}

func validatePayment(meta domain.EventMeta, payment domain.PaymentSucceeded) error {
	if meta.EventType != "billing.payment.succeeded.v1" || !validUUIDs(meta.EventID, meta.AggregateID, meta.CorrelationID, payment.PaymentID, payment.OrderID, payment.UserID) {
		return &ContractError{Code: "invalid_payment_identity"}
	}
	if meta.AggregateID != payment.PaymentID || !meta.OccurredAt.Equal(payment.PaidAt) || payment.AmountMinor <= 0 || len(payment.PlanID) == 0 || len(payment.PlanID) > 128 || strings.TrimSpace(payment.PlanID) != payment.PlanID || !validCurrency(payment.Currency) || payment.PaidAt.IsZero() {
		return &ContractError{Code: "invalid_payment_data"}
	}
	return nil
}

func validateRefund(meta domain.EventMeta, refund domain.RefundSucceeded) error {
	if meta.EventType != "billing.refund.succeeded.v1" || !validUUIDs(meta.EventID, meta.AggregateID, meta.CorrelationID, refund.RefundID, refund.PaymentID, refund.OrderID, refund.UserID) {
		return &ContractError{Code: "invalid_refund_identity"}
	}
	if meta.AggregateID != refund.RefundID || !meta.OccurredAt.Equal(refund.RefundedAt) || refund.RefundScope != "full" || refund.AmountMinor <= 0 || !validCurrency(refund.Currency) || refund.RefundedAt.IsZero() {
		return &ContractError{Code: "invalid_refund_data"}
	}
	return nil
}

func validateOrder(payment domain.PaymentSucceeded, order domain.Order) error {
	snapshot := order.PlanSnapshot
	if order.OrderID != payment.OrderID || order.UserID != payment.UserID || order.Status != "paid" || order.AmountMinor != payment.AmountMinor || order.Currency != payment.Currency || snapshot.PlanID != payment.PlanID || snapshot.AmountMinor != payment.AmountMinor || snapshot.Currency != payment.Currency {
		return &ContractError{Code: "billing_order_mismatch"}
	}
	if snapshot.DurationDays < 1 || snapshot.DurationDays > 3650 || snapshot.GracePeriodHours < 0 || snapshot.GracePeriodHours > 720 || len(strings.TrimSpace(snapshot.Region)) < 2 || len(snapshot.Region) > 64 || strings.TrimSpace(snapshot.Region) != snapshot.Region {
		return &ContractError{Code: "invalid_order_snapshot"}
	}
	return nil
}

func validUUIDs(values ...string) bool {
	for _, value := range values {
		if !uuidPattern.MatchString(value) {
			return false
		}
	}
	return true
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func RetryBackoff(base time.Duration, attempt int, key string) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func classifyWorkerError(err error) string {
	if code, ok := ContractErrorCode(err); ok {
		return code
	}
	return fmt.Sprintf("%T", err)
}
