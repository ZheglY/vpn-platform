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

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

const maxRetryBodyBytes = 8 << 10

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
)

type Service interface {
	GetJob(context.Context, string) (domain.Job, error)
	RequestRetry(context.Context, string, string, string) (domain.Job, bool, error)
	ListDeadLetters(context.Context, int) ([]domain.DeadLetter, error)
}

func (h *Handler) ListDeadLetters(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 {
			httperror.Write(w, r, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	items, err := h.service.ListDeadLetters(r.Context(), limit)
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "dead_letters_unavailable", "dead-letter metadata is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dead_letters": items})
}

type Handler struct{ service Service }

func New(service Service) *Handler { return &Handler{service: service} }

func (h *Handler) GetNotification(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("notification_id"))
	if !uuidPattern.MatchString(id) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_notification_id", "notification id is invalid")
		return
	}
	job, err := h.service.GetJob(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "not_found", "notification was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "notification_read_failed", "notification lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) RetryNotification(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("notification_id"))
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !uuidPattern.MatchString(id) || !keyPattern.MatchString(key) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_request", "notification id or idempotency key is invalid")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeStrict(r.Body, &request); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if len(request.Reason) < 3 || len(request.Reason) > 512 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_reason", "reason must contain 3 to 512 characters")
		return
	}
	sum := sha256.Sum256([]byte("notification.retry\n" + id + "\n" + request.Reason))
	job, replay, err := h.service.RequestRetry(r.Context(), id, key, hex.EncodeToString(sum[:]))
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]any{
			"notification": map[string]any{
				"notification_id": job.NotificationID, "notification_type": job.NotificationType,
				"status": job.Status, "attempts": job.Attempts, "max_attempts": job.MaxAttempts,
			},
			"replay": replay,
		})
	case errors.Is(err, domain.ErrNotFound):
		httperror.Write(w, r, http.StatusNotFound, "not_found", "notification was not found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		httperror.Write(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key conflicts with another request")
	case errors.Is(err, domain.ErrRetryNotAllowed):
		httperror.Write(w, r, http.StatusConflict, "retry_not_allowed", "notification is not retryable")
	default:
		httperror.Write(w, r, http.StatusInternalServerError, "notification_retry_failed", "notification retry failed")
	}
}

func decodeStrict(body io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxRetryBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxRetryBodyBytes {
		return fmt.Errorf("request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON documents")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
