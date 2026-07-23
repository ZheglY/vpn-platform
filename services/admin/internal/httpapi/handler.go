package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httpauth"
	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/admin/internal/application"
	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
	"github.com/ZheglY/vpn-platform/services/admin/internal/owner"
)

const maxMutationBodyBytes = 8 << 10

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type Service interface {
	Authenticate(context.Context, string, string) (domain.Principal, error)
	Authorize(domain.Principal, string) error
	Execute(context.Context, application.ExecuteInput, application.OwnerCall) (domain.Action, error)
	ListAudit(context.Context, domain.Principal, int) ([]domain.AuditEvent, error)
}

type Owners interface {
	RetryNotification(context.Context, string, string, string, string) (owner.NotificationRetryResult, error)
	RevokeSubscription(context.Context, string, string, string, string) (owner.SubscriptionRevokeResult, error)
	RecoverAccess(context.Context, string, string, string) (owner.AccessRecoveryResult, error)
	ReadIdentity(context.Context, string) (json.RawMessage, error)
	ReadConsent(context.Context, string, string, string) (json.RawMessage, error)
	ReadOrder(context.Context, string, string) (json.RawMessage, error)
	ReadPayment(context.Context, string, string, string) (json.RawMessage, error)
	ReadSubscription(context.Context, string) (json.RawMessage, error)
	ReadAccess(context.Context, string) (json.RawMessage, error)
	ReadNotification(context.Context, string) (json.RawMessage, error)
	ReadNotificationDeadLetters(context.Context, int) (json.RawMessage, error)
	ReadProvisioning(context.Context, string) (json.RawMessage, error)
	ReadHealthSummary(context.Context) owner.HealthSummary
}

type Handler struct {
	service     Service
	owners      Owners
	trustDomain string
	environment string
}

func New(service Service, owners Owners, trustDomain, environment string) *Handler {
	return &Handler{service: service, owners: owners, trustDomain: trustDomain, environment: environment}
}

type AuthenticatedHandler func(http.ResponseWriter, *http.Request, domain.Principal)

func (h *Handler) Protect(permission string, next AuthenticatedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := httpauth.AdminIdentityFromTLS(r.TLS, h.trustDomain, h.environment)
		if !ok {
			if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
				httperror.Write(w, r, http.StatusForbidden, "forbidden", "administrator identity is not allowed")
				return
			}
			httperror.Write(w, r, http.StatusUnauthorized, "unauthenticated", "verified administrator certificate is required")
			return
		}
		principal, err := h.service.Authenticate(r.Context(), identity.SPIFFEID, identity.Principal)
		if err != nil {
			httperror.Write(w, r, http.StatusForbidden, "forbidden", "administrator is not enabled")
			return
		}
		if err := h.service.Authorize(principal, permission); err != nil {
			httperror.Write(w, r, http.StatusForbidden, "forbidden", "permission is not granted")
			return
		}
		next(w, r, principal)
	})
}

func (h *Handler) GetIdentity(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	h.proxyUUID(w, r, r.PathValue("user_id"), h.owners.ReadIdentity)
}

func (h *Handler) GetConsent(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	userID := strings.TrimSpace(r.PathValue("user_id"))
	documentType, documentVersion := strings.TrimSpace(r.PathValue("document_type")), strings.TrimSpace(r.PathValue("document_version"))
	if !uuidPattern.MatchString(userID) || !namePattern.MatchString(documentType) || !namePattern.MatchString(documentVersion) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "consent lookup is invalid")
		return
	}
	payload, err := h.owners.ReadConsent(r.Context(), userID, documentType, documentVersion)
	h.writeOwnerRead(w, r, payload, err)
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	userID, orderID := strings.TrimSpace(r.PathValue("user_id")), strings.TrimSpace(r.PathValue("order_id"))
	if !uuidPattern.MatchString(userID) || !uuidPattern.MatchString(orderID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "order lookup is invalid")
		return
	}
	payload, err := h.owners.ReadOrder(r.Context(), userID, orderID)
	h.writeOwnerRead(w, r, payload, err)
}

func (h *Handler) GetPayment(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	userID, orderID, paymentID := strings.TrimSpace(r.PathValue("user_id")), strings.TrimSpace(r.PathValue("order_id")), strings.TrimSpace(r.PathValue("payment_id"))
	if !uuidPattern.MatchString(userID) || !uuidPattern.MatchString(orderID) || !uuidPattern.MatchString(paymentID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "payment lookup is invalid")
		return
	}
	payload, err := h.owners.ReadPayment(r.Context(), userID, orderID, paymentID)
	h.writeOwnerRead(w, r, payload, err)
}

func (h *Handler) GetSubscription(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	h.proxyUUID(w, r, r.PathValue("user_id"), h.owners.ReadSubscription)
}

func (h *Handler) GetAccess(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	h.proxyUUID(w, r, r.PathValue("subscription_id"), h.owners.ReadAccess)
}

func (h *Handler) GetNotification(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	h.proxyUUID(w, r, r.PathValue("notification_id"), h.owners.ReadNotification)
}

func (h *Handler) GetProvisioning(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	h.proxyUUID(w, r, r.PathValue("credential_id"), h.owners.ReadProvisioning)
}

func (h *Handler) ListNotificationDeadLetters(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	payload, err := h.owners.ReadNotificationDeadLetters(r.Context(), limit)
	h.writeOwnerRead(w, r, payload, err)
}

func (h *Handler) GetHealthSummary(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	writeJSON(w, http.StatusOK, h.owners.ReadHealthSummary(r.Context()))
}

func (h *Handler) RetryNotification(w http.ResponseWriter, r *http.Request, principal domain.Principal) {
	targetID, reason, _, ok := decodeMutation(w, r, false)
	if !ok {
		return
	}
	h.execute(w, r, principal, domain.ActionNotificationRetry, "notification.retry", "notification", targetID, reason, "",
		func(ctx context.Context, actionID, correlationID string) (any, error) {
			return h.owners.RetryNotification(ctx, targetID, actionID, correlationID, reason)
		})
}

func (h *Handler) RevokeSubscription(w http.ResponseWriter, r *http.Request, principal domain.Principal) {
	targetID, reason, reasonCode, ok := decodeMutation(w, r, true)
	if !ok {
		return
	}
	if reasonCode != "admin_block" && reasonCode != "abuse" && reasonCode != "deleted" {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_reason_code", "reason code is invalid")
		return
	}
	h.execute(w, r, principal, domain.ActionSubscriptionRevoke, "subscription.revoke", "subscription", targetID, reason, reasonCode,
		func(ctx context.Context, actionID, correlationID string) (any, error) {
			return h.owners.RevokeSubscription(ctx, targetID, actionID, correlationID, reasonCode)
		})
}

func (h *Handler) RecoverAccess(w http.ResponseWriter, r *http.Request, principal domain.Principal) {
	targetID, reason, _, ok := decodeMutation(w, r, false)
	if !ok {
		return
	}
	h.execute(w, r, principal, domain.ActionAccessRecover, "access.provisioning.recover", "credential", targetID, reason, "",
		func(ctx context.Context, actionID, correlationID string) (any, error) {
			return h.owners.RecoverAccess(ctx, targetID, actionID, correlationID)
		})
}

func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request, principal domain.Principal) {
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	events, err := h.service.ListAudit(r.Context(), principal, limit)
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "audit_unavailable", "audit is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 {
			httperror.Write(w, r, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func (h *Handler) execute(w http.ResponseWriter, r *http.Request, principal domain.Principal, action, permission, targetType, targetID, reason, reasonCode string, call application.OwnerCall) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !keyPattern.MatchString(key) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_idempotency_key", "valid Idempotency-Key is required")
		return
	}
	requestHash := sha256.Sum256([]byte(action + "\n" + targetID + "\n" + reason + "\n" + reasonCode))
	result, err := h.service.Execute(r.Context(), application.ExecuteInput{
		Principal: principal, Action: action, Permission: permission, TargetType: targetType, TargetID: targetID,
		Reason: reason, ReasonCode: reasonCode, IdempotencyKey: key, RequestSHA256: hex.EncodeToString(requestHash[:]),
		RequestID: requestid.FromRequest(r),
	}, call)
	if errors.Is(err, domain.ErrIdempotencyConflict) {
		httperror.Write(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key conflicts with another request")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"action": result, "error_code": "owner_operation_failed"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeMutation(w http.ResponseWriter, r *http.Request, requireReasonCode bool) (string, string, string, bool) {
	targetID := mutationTargetID(r)
	var request struct {
		Reason     string `json:"reason"`
		ReasonCode string `json:"reason_code,omitempty"`
	}
	if !uuidPattern.MatchString(targetID) || decodeStrict(r.Body, &request) != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "mutation request is invalid")
		return "", "", "", false
	}
	request.Reason = strings.TrimSpace(request.Reason)
	request.ReasonCode = strings.TrimSpace(request.ReasonCode)
	if len(request.Reason) < 3 || len(request.Reason) > 512 || unsafeReason(request.Reason) || requireReasonCode && request.ReasonCode == "" || !requireReasonCode && request.ReasonCode != "" {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_reason", "human-readable reason is invalid")
		return "", "", "", false
	}
	return targetID, request.Reason, request.ReasonCode, true
}

func unsafeReason(reason string) bool {
	lower := strings.ToLower(reason)
	return strings.Contains(lower, "vless://") || strings.Contains(lower, "/s/") || strings.Contains(lower, "subscription_url") || strings.Contains(lower, "vless_uuid") || strings.Contains(lower, "ciphertext")
}

func mutationTargetID(r *http.Request) string {
	for _, name := range []string{"notification_id", "subscription_id", "credential_id"} {
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) proxyUUID(w http.ResponseWriter, r *http.Request, rawID string, read func(context.Context, string) (json.RawMessage, error)) {
	id := strings.TrimSpace(rawID)
	if !uuidPattern.MatchString(id) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_id", "resource id is invalid")
		return
	}
	payload, err := read(r.Context(), id)
	h.writeOwnerRead(w, r, payload, err)
}

func (h *Handler) writeOwnerRead(w http.ResponseWriter, r *http.Request, payload json.RawMessage, err error) {
	if err != nil {
		var ownerErr *domain.OwnerError
		if errors.As(err, &ownerErr) && ownerErr.Code == "owner_not_found" {
			httperror.Write(w, r, http.StatusNotFound, "not_found", "resource was not found")
			return
		}
		httperror.Write(w, r, http.StatusBadGateway, "owner_unavailable", "owning service is unavailable")
		return
	}
	if application.ValidateSafePayload(payload) != nil {
		httperror.Write(w, r, http.StatusBadGateway, "unsafe_owner_response", "owning service response was rejected")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(payload)
}

func decodeStrict(body io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxMutationBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxMutationBodyBytes {
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
