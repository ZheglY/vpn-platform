package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/ZheglY/vpn-platform/services/access/internal/application"
	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

const maxAdminRecoveryBodyBytes = 8 << 10

var (
	uuidPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
)

type Service interface {
	IssueSubscriptionURL(rctx context.Context, subscriptionID, idempotencyKey, operation string) (string, error)
	GetAccessStatus(context.Context, string) (domain.AccessStatus, error)
	RecoverProvisioning(context.Context, domain.AdminRecoveryInput) (domain.AdminRecoveryResult, error)
	GetProfile(context.Context, string) (application.Profile, error)
	GetProvisioningMaterial(context.Context, string, string) (application.ProvisioningMaterial, error)
}

type PublicRateLimiter interface {
	Allow(context.Context, string, string) (bool, error)
}

type Handler struct {
	service      Service
	profileTitle string
	supportURL   string
	updateHours  int
	limiter      PublicRateLimiter
}

func New(service Service, profileTitle, supportURL string, updateHours int, limiter PublicRateLimiter) *Handler {
	return &Handler{service: service, profileTitle: profileTitle, supportURL: supportURL, updateHours: updateHours, limiter: limiter}
}

func (h *Handler) IssueSubscriptionURL(w http.ResponseWriter, r *http.Request) {
	h.handleURLCommand(w, r, "issue")
}

func (h *Handler) RotateSubscriptionURL(w http.ResponseWriter, r *http.Request) {
	h.handleURLCommand(w, r, "rotate")
}

func (h *Handler) handleURLCommand(w http.ResponseWriter, r *http.Request, operation string) {
	subscriptionID := strings.TrimSpace(r.PathValue("subscription_id"))
	if !uuidPattern.MatchString(subscriptionID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_subscription_id", "subscription id is invalid")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_idempotency_key", "idempotency key is invalid")
		return
	}
	subscriptionURL, err := h.service.IssueSubscriptionURL(r.Context(), subscriptionID, idempotencyKey, operation)
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"subscription_url": subscriptionURL})
	case errors.Is(err, domain.ErrNotReady):
		httperror.Write(w, r, http.StatusConflict, "access_not_ready", "VPN access is not ready")
	case errors.Is(err, domain.ErrAlreadyIssued):
		httperror.Write(w, r, http.StatusConflict, "subscription_url_already_issued", "subscription URL was already issued")
	case errors.Is(err, domain.ErrIdempotencyReplay):
		httperror.Write(w, r, http.StatusConflict, "idempotency_response_unavailable", "the one-time response was already delivered")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		httperror.Write(w, r, http.StatusConflict, "idempotency_key_conflict", "idempotency key conflicts with another request")
	case errors.Is(err, domain.ErrNotFound):
		httperror.Write(w, r, http.StatusNotFound, "access_not_found", "access was not found")
	default:
		httperror.Write(w, r, http.StatusInternalServerError, "access_unavailable", "access is unavailable")
	}
}

func (h *Handler) GetAccessStatus(w http.ResponseWriter, r *http.Request) {
	subscriptionID := strings.TrimSpace(r.PathValue("subscription_id"))
	if !uuidPattern.MatchString(subscriptionID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_subscription_id", "subscription id is invalid")
		return
	}
	status, err := h.service.GetAccessStatus(r.Context(), subscriptionID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "access_not_found", "access was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "access_unavailable", "access is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(status)
}

func (h *Handler) GetProvisioningMaterial(w http.ResponseWriter, r *http.Request) {
	credentialID := strings.TrimSpace(r.PathValue("credential_id"))
	if !uuidPattern.MatchString(credentialID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_credential_id", "credential id is invalid")
		return
	}
	actorService := "dev-insecure"
	if identity, ok := httpauth.FromContext(r.Context()); ok {
		actorService = identity.Name
	}
	material, err := h.service.GetProvisioningMaterial(r.Context(), credentialID, actorService)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "credential_not_found", "credential was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "credential_unavailable", "credential is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(material)
}

func (h *Handler) RecoverProvisioning(w http.ResponseWriter, r *http.Request) {
	credentialID := strings.TrimSpace(r.PathValue("credential_id"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	var request struct {
		ActionID      string `json:"action_id"`
		CorrelationID string `json:"correlation_id"`
	}
	if !uuidPattern.MatchString(credentialID) || !idempotencyKeyPattern.MatchString(idempotencyKey) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "provisioning recovery request is invalid")
		return
	}
	if err := decodeStrictJSON(r.Body, &request); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	request.ActionID = strings.TrimSpace(request.ActionID)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	if !uuidPattern.MatchString(request.ActionID) || !uuidPattern.MatchString(request.CorrelationID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "provisioning recovery request is invalid")
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("access.provisioning.recover\n"+credentialID+"\n"+request.ActionID+"\n"+request.CorrelationID)))
	result, err := h.service.RecoverProvisioning(r.Context(), domain.AdminRecoveryInput{
		CredentialID: credentialID, IdempotencyKey: idempotencyKey, RequestSHA256: hash,
		ActionID: request.ActionID, CorrelationID: request.CorrelationID,
	})
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	case errors.Is(err, domain.ErrNotFound):
		httperror.Write(w, r, http.StatusNotFound, "credential_not_found", "credential was not found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		httperror.Write(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key conflicts with another request")
	case errors.Is(err, domain.ErrRecoveryNotAllowed):
		httperror.Write(w, r, http.StatusConflict, "recovery_not_allowed", "provisioning recovery is not allowed")
	default:
		httperror.Write(w, r, http.StatusInternalServerError, "recovery_failed", "provisioning recovery failed")
	}
}

func decodeStrictJSON(body io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxAdminRecoveryBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxAdminRecoveryBodyBytes {
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

func (h *Handler) GetHappSubscription(w http.ResponseWriter, r *http.Request) {
	setPublicSecurityHeaders(w)
	token := r.PathValue("token")
	if h.limiter != nil {
		allowed, err := h.limiter.Allow(r.Context(), r.RemoteAddr, token)
		if err != nil {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}
	profile, err := h.service.GetProfile(r.Context(), token)
	if errors.Is(err, domain.ErrNotFound) {
		writeUnavailable(w)
		return
	}
	if err != nil {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("profile-title", h.profileTitle)
	w.Header().Set("profile-update-interval", strconv.Itoa(h.updateHours))
	w.Header().Set("subscription-userinfo", fmt.Sprintf("upload=0; download=0; total=0; expire=%d", profile.ExpiresAt.Unix()))
	if h.supportURL != "" {
		w.Header().Set("support-url", h.supportURL)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(profile.Body))
}

func (h *Handler) GetUnavailableHappSubscription(w http.ResponseWriter, _ *http.Request) {
	setPublicSecurityHeaders(w)
	writeUnavailable(w)
}

func setPublicSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("subscription unavailable\n"))
}
