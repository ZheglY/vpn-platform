package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type Billing interface {
	CreateOrder(context.Context, string, string, string, string, string) (domain.Order, bool, error)
	GetOrder(context.Context, string, string) (domain.Order, error)
	CreatePayment(context.Context, string, string, string) (domain.Payment, bool, error)
}

type Inbox interface {
	InsertWebhook(context.Context, domain.WebhookNotification) (bool, error)
}

type Handler struct {
	billing Billing
	inbox   Inbox
}

func New(billing Billing, inbox Inbox) *Handler { return &Handler{billing: billing, inbox: inbox} }

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !isUUID(userID) || !validIdempotencyKey(idempotencyKey) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "user id or idempotency key is invalid")
		return
	}
	var request struct {
		PlanID               string `json:"plan_id"`
		Region               string `json:"region"`
		AcceptedTermsVersion string `json:"accepted_terms_version"`
	}
	if err := decodeStrict(r.Body, &request); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	request.PlanID = strings.TrimSpace(request.PlanID)
	request.Region = strings.TrimSpace(request.Region)
	request.AcceptedTermsVersion = strings.TrimSpace(request.AcceptedTermsVersion)
	if request.PlanID == "" || len(request.PlanID) > 128 || len(request.Region) < 2 || len(request.Region) > 64 || request.AcceptedTermsVersion == "" || len(request.AcceptedTermsVersion) > 64 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_order", "order request is invalid")
		return
	}
	order, created, err := h.billing.CreateOrder(r.Context(), userID, request.PlanID, request.Region, request.AcceptedTermsVersion, idempotencyKey)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, order)
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	userID, orderID := r.PathValue("user_id"), r.PathValue("order_id")
	if !isUUID(userID) || !isUUID(orderID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "user id or order id is invalid")
		return
	}
	order, err := h.billing.GetOrder(r.Context(), userID, orderID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (h *Handler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	userID, orderID := r.PathValue("user_id"), r.PathValue("order_id")
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !isUUID(userID) || !isUUID(orderID) || !validIdempotencyKey(idempotencyKey) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "payment request is invalid")
		return
	}
	payment, created, err := h.billing.CreatePayment(r.Context(), userID, orderID, idempotencyKey)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	status := http.StatusOK
	if payment.Status == domain.PaymentStatusVerificationPending || payment.Status == domain.PaymentStatusCreated {
		status = http.StatusAccepted
	} else if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, payment)
}

func (h *Handler) YooKassaWebhook(w http.ResponseWriter, r *http.Request) {
	var notification struct {
		Type   string          `json:"type"`
		Event  string          `json:"event"`
		Object json.RawMessage `json:"object"`
	}
	if err := decodeStrict(r.Body, &notification); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_webhook", "webhook payload is invalid")
		return
	}
	if notification.Type != "notification" || (notification.Event != "payment.succeeded" && notification.Event != "payment.canceled") {
		httperror.Write(w, r, http.StatusBadRequest, "unsupported_webhook", "webhook event is unsupported")
		return
	}
	var object struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(notification.Object, &object); err != nil || object.ID == "" || len(object.ID) > 128 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_webhook", "webhook object is invalid")
		return
	}
	expectedStatus := strings.TrimPrefix(notification.Event, "payment.")
	if object.Status != expectedStatus {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_webhook", "webhook status does not match event")
		return
	}
	inboxID, err := cryptoutil.RandomUUID()
	if err != nil {
		httperror.Write(w, r, http.StatusServiceUnavailable, "webhook_unavailable", "webhook storage is unavailable")
		return
	}
	_, err = h.inbox.InsertWebhook(r.Context(), domain.WebhookNotification{InboxID: inboxID, EventType: notification.Event, ProviderObjectID: object.ID, ObservedStatus: object.Status})
	if err != nil {
		httperror.Write(w, r, http.StatusServiceUnavailable, "webhook_unavailable", "webhook storage is unavailable")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httperror.Write(w, r, http.StatusNotFound, "not_found", "billing resource was not found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		httperror.Write(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was used for a different request")
	case errors.Is(err, domain.ErrStateConflict):
		httperror.Write(w, r, http.StatusConflict, "state_conflict", "billing resource is in an incompatible state")
	case errors.Is(err, domain.ErrUserUnavailable):
		httperror.Write(w, r, http.StatusForbidden, "user_unavailable", "user is unavailable")
	case errors.Is(err, domain.ErrConsentRequired):
		httperror.Write(w, r, http.StatusForbidden, "consent_required", "required consent is missing")
	default:
		var providerErr *domain.ProviderError
		if errors.As(err, &providerErr) {
			httperror.Write(w, r, http.StatusBadGateway, "payment_provider_error", "payment provider request failed")
			return
		}
		httperror.Write(w, r, http.StatusInternalServerError, "billing_error", "billing operation failed")
	}
}

func decodeStrict(body io.Reader, value any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func validIdempotencyKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}
func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}
