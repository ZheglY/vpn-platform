package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type Service struct {
	store        domain.Store
	identity     domain.IdentityVerifier
	catalog      domain.Catalog
	provider     domain.Provider
	shopID       string
	createWindow time.Duration
	retryDelay   time.Duration
	now          func() time.Time
}

func NewService(store domain.Store, identity domain.IdentityVerifier, catalog domain.Catalog, provider domain.Provider, shopID string, createWindow, retryDelay time.Duration) *Service {
	return &Service{store: store, identity: identity, catalog: catalog, provider: provider, shopID: shopID, createWindow: createWindow, retryDelay: retryDelay, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) CreateOrder(ctx context.Context, userID, planID, region, termsVersion, idempotencyKey string) (domain.Order, bool, error) {
	if err := s.identity.VerifyActiveWithConsent(ctx, userID, termsVersion); err != nil {
		return domain.Order{}, false, err
	}
	snapshot, err := s.catalog.GetPlan(ctx, planID, region)
	if err != nil {
		return domain.Order{}, false, err
	}
	requestHash := hashParts(userID, planID, region, termsVersion)
	return s.store.CreateOrder(ctx, domain.CreateOrderInput{UserID: userID, PlanID: planID, Region: region, AcceptedTermsVersion: termsVersion, IdempotencyKey: idempotencyKey, RequestHash: requestHash, Snapshot: snapshot})
}

func (s *Service) GetOrder(ctx context.Context, userID, orderID string) (domain.Order, error) {
	return s.store.GetOrder(ctx, userID, orderID)
}

func (s *Service) CreatePayment(ctx context.Context, userID, orderID, idempotencyKey string) (domain.Payment, bool, error) {
	order, err := s.store.GetOrder(ctx, userID, orderID)
	if err != nil {
		return domain.Payment{}, false, err
	}
	if err := s.identity.VerifyActiveWithConsent(ctx, userID, order.AcceptedTermsVersion); err != nil {
		return domain.Payment{}, false, err
	}
	operation, created, err := s.store.CreatePayment(ctx, domain.CreatePaymentInput{UserID: userID, OrderID: orderID, IdempotencyKey: idempotencyKey, RequestHash: hashParts(userID, orderID)}, "yookassa", s.createWindow)
	if err != nil {
		return domain.Payment{}, false, err
	}
	if operation.Status != domain.PaymentStatusCreated && operation.Status != domain.PaymentStatusVerificationPending {
		return operation.Payment, created, nil
	}
	if operation.ProviderPaymentID != nil {
		return operation.Payment, created, nil
	}
	payment, err := s.createAtProvider(ctx, operation)
	return payment.Payment, created, err
}

func (s *Service) createAtProvider(ctx context.Context, operation domain.PaymentOperation) (domain.PaymentOperation, error) {
	providerPayment, err := s.provider.CreatePayment(ctx, domain.ProviderCreateRequest{IdempotencyKey: operation.ProviderIdempotencyKey, OrderID: operation.OrderID, PaymentID: operation.PaymentID, AmountMinor: operation.AmountMinor, Currency: operation.Currency})
	if err != nil {
		if domain.IsProviderRetryable(err) {
			_ = s.store.MarkProviderCreateAmbiguous(ctx, operation.PaymentID, providerErrorCode(err), s.retryDelay)
			current, getErr := s.store.GetPayment(ctx, operation.PaymentID)
			if getErr != nil {
				return domain.PaymentOperation{}, getErr
			}
			return current, nil
		}
		_ = s.store.MarkProviderCreateFailed(ctx, operation.PaymentID, providerErrorCode(err))
		return domain.PaymentOperation{}, err
	}
	if err := s.validateProviderPayment(operation, providerPayment, true); err != nil {
		_ = s.store.MarkProviderCreateFailed(ctx, operation.PaymentID, "provider_mismatch")
		return domain.PaymentOperation{}, err
	}
	return s.store.ApplyProviderCreate(ctx, operation.PaymentID, providerPayment)
}

func (s *Service) validateProviderPayment(expected domain.PaymentOperation, actual domain.ProviderPayment, requireConfirmation bool) error {
	if expected.ProviderPaymentID != nil && *expected.ProviderPaymentID != actual.ProviderPaymentID {
		return fmt.Errorf("provider payment id mismatch")
	}
	if !actual.Test || actual.AccountID != s.shopID || actual.AmountMinor != expected.AmountMinor || actual.Currency != expected.Currency || actual.MetadataOrderID != expected.OrderID || actual.MetadataPaymentID != expected.PaymentID {
		return fmt.Errorf("provider payment verification mismatch")
	}
	if requireConfirmation && actual.Status == "pending" {
		if actual.ConfirmationURL == nil {
			return fmt.Errorf("provider confirmation URL missing")
		}
		parsed, err := url.Parse(*actual.ConfirmationURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("provider confirmation URL invalid")
		}
	}
	return nil
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func providerErrorCode(err error) string {
	if providerErr, ok := err.(*domain.ProviderError); ok {
		return providerErr.Code
	}
	return "provider_error"
}
