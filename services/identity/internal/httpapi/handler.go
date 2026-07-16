package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/identity/internal/domain"
)

type Handler struct {
	store domain.Store
}

func New(store domain.Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) UpsertTelegramIdentity(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegram_id"), 10, 64)
	if err != nil || telegramID <= 0 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_telegram_id", "telegram id must be a positive integer")
		return
	}

	var req upsertTelegramRequest
	if err := decodeStrictJSON(r.Body, &req); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	username := normalizeOptional(req.Username)
	displayName := normalizeOptional(req.DisplayName)
	languageCode := normalizeOptional(req.LanguageCode)
	locale := normalizeOptional(req.Locale)
	if exceeds(username, 64) || exceeds(displayName, 256) || exceeds(languageCode, 16) || exceeds(locale, 32) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_telegram_profile", "telegram profile fields exceed allowed length")
		return
	}

	user, err := h.store.UpsertTelegramIdentity(r.Context(), domain.TelegramProfile{
		TelegramUserID: telegramID,
		Username:       username,
		DisplayName:    displayName,
		LanguageCode:   languageCode,
		Locale:         locale,
	})
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "identity_upsert_failed", "identity upsert failed")
		return
	}

	writeJSON(w, http.StatusOK, userResponseFromDomain(user))
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if !isUUID(userID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_user_id", "user id must be a UUID")
		return
	}
	user, err := h.store.GetUser(r.Context(), userID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "not_found", "user was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "identity_get_failed", "identity lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, userResponseFromDomain(user))
}

func (h *Handler) AcceptConsent(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if !isUUID(userID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_user_id", "user id must be a UUID")
		return
	}
	var req acceptConsentRequest
	if err := decodeStrictJSON(r.Body, &req); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	documentType := strings.TrimSpace(req.DocumentType)
	documentVersion := strings.TrimSpace(req.DocumentVersion)
	if documentType == "" || documentVersion == "" || len(documentType) > 64 || len(documentVersion) > 64 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_consent", "document type and version are required")
		return
	}

	if _, err := h.store.GetUser(r.Context(), userID); errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "not_found", "user was not found")
		return
	} else if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "identity_get_failed", "identity lookup failed")
		return
	}

	if err := h.store.AcceptConsent(r.Context(), domain.ConsentInput{
		UserID:          userID,
		DocumentType:    documentType,
		DocumentVersion: documentVersion,
		Source:          "telegram",
	}); err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "consent_accept_failed", "consent acceptance failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HasConsent(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if !isUUID(userID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_user_id", "user id must be a UUID")
		return
	}
	documentType := strings.TrimSpace(r.PathValue("document_type"))
	documentVersion := strings.TrimSpace(r.PathValue("document_version"))
	if documentType == "" || documentVersion == "" || len(documentType) > 64 || len(documentVersion) > 64 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_consent", "document type and version are required")
		return
	}
	if _, err := h.store.GetUser(r.Context(), userID); errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "not_found", "user was not found")
		return
	} else if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "identity_get_failed", "identity lookup failed")
		return
	}
	accepted, err := h.store.HasConsent(r.Context(), userID, documentType, documentVersion)
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "consent_check_failed", "consent check failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": accepted})
}

type upsertTelegramRequest struct {
	Username     *string `json:"username"`
	DisplayName  *string `json:"display_name"`
	LanguageCode *string `json:"language_code"`
	Locale       *string `json:"locale"`
}

type acceptConsentRequest struct {
	DocumentType    string `json:"document_type"`
	DocumentVersion string `json:"document_version"`
}

type userResponse struct {
	UserID   string  `json:"user_id"`
	Status   string  `json:"status"`
	Locale   *string `json:"locale,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}

func userResponseFromDomain(user domain.User) userResponse {
	return userResponse{
		UserID:   user.ID,
		Status:   user.Status,
		Locale:   user.Locale,
		Timezone: user.Timezone,
	}
}

func normalizeOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func exceeds(value *string, max int) bool {
	return value != nil && len(*value) > max
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func decodeStrictJSON(body io.Reader, out any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON document")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		panic(fmt.Errorf("write JSON response: %w", err))
	}
}
