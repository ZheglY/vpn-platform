package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

const maxAdminRevokeBodyBytes = 8 << 10

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
)

type Handler struct{ store domain.Store }

func New(store domain.Store) *Handler { return &Handler{store: store} }

func (h *Handler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("user_id"))
	if !uuidPattern.MatchString(userID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_user_id", "user id is invalid")
		return
	}
	subscription, err := h.store.GetSubscription(r.Context(), userID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "subscription_not_found", "subscription was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "subscription_unavailable", "subscription is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(subscription)
}

func (h *Handler) GetPlacement(w http.ResponseWriter, r *http.Request) {
	subscriptionID := strings.TrimSpace(r.PathValue("subscription_id"))
	if !uuidPattern.MatchString(subscriptionID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_subscription_id", "subscription id is invalid")
		return
	}
	placement, err := h.store.GetPlacement(r.Context(), subscriptionID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "placement_not_found", "placement is unavailable")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "placement_unavailable", "placement is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(placement)
}

func (h *Handler) AdminRevoke(w http.ResponseWriter, r *http.Request) {
	subscriptionID := strings.TrimSpace(r.PathValue("subscription_id"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	var request struct {
		ActionID      string `json:"action_id"`
		CorrelationID string `json:"correlation_id"`
		ReasonCode    string `json:"reason_code"`
	}
	if !uuidPattern.MatchString(subscriptionID) || !keyPattern.MatchString(idempotencyKey) || decodeStrict(r.Body, &request) != nil {
		httpError(w, r, http.StatusBadRequest, "invalid_request", "admin revoke request is invalid")
		return
	}
	request.ActionID = strings.TrimSpace(request.ActionID)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.ReasonCode = strings.TrimSpace(request.ReasonCode)
	if !uuidPattern.MatchString(request.ActionID) || !uuidPattern.MatchString(request.CorrelationID) || !validAdminReason(request.ReasonCode) {
		httpError(w, r, http.StatusBadRequest, "invalid_request", "admin revoke request is invalid")
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("subscription.revoke\n"+subscriptionID+"\n"+request.ActionID+"\n"+request.CorrelationID+"\n"+request.ReasonCode)))
	result, err := h.store.AdminRevoke(r.Context(), domain.AdminRevokeInput{
		SubscriptionID: subscriptionID, IdempotencyKey: idempotencyKey, RequestSHA256: hash,
		ActionID: request.ActionID, CorrelationID: request.CorrelationID, ReasonCode: request.ReasonCode,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, domain.ErrNotFound):
		httpError(w, r, http.StatusNotFound, "subscription_not_found", "subscription was not found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		httpError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key conflicts with another request")
	case errors.Is(err, domain.ErrAdminRevokeNotAllowed):
		httpError(w, r, http.StatusConflict, "revoke_not_allowed", "subscription cannot be revoked in its current state")
	default:
		httpError(w, r, http.StatusInternalServerError, "revoke_failed", "subscription revoke failed")
	}
}

func validAdminReason(reason string) bool {
	return reason == "admin_block" || reason == "abuse" || reason == "deleted"
}

func decodeStrict(body io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxAdminRevokeBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxAdminRevokeBodyBytes {
		return fmt.Errorf("request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func httpError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	// Keep owner API failures bounded; upstream bodies are never persisted by admin-service.
	httperror.Write(w, r, status, code, message)
}
