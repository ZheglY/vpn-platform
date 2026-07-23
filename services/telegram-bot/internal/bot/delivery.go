package bot

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
	"time"
	"unicode/utf8"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	telegramapi "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/telegram"
)

const maxDeliveryBodyBytes = 32 << 10

var deliveryUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

var notificationTypes = map[string]struct{}{
	"payment_confirmed": {}, "subscription_extended": {}, "subscription_grace": {},
	"subscription_expired": {}, "subscription_revoked": {}, "refund_confirmed": {},
	"access_ready": {}, "access_degraded": {}, "provisioning_failed": {},
}

type DeliveryStore interface {
	StartDelivery(context.Context, string, string, string) (string, error)
	CompleteDelivery(context.Context, string, string, string) error
	ReleaseDelivery(context.Context, string, string, string) error
}

type NotificationTelegram interface {
	SendHTMLMessage(context.Context, int64, string) error
}

type DeliveryHandler struct {
	store    DeliveryStore
	telegram NotificationTelegram
}

func NewDeliveryHandler(store DeliveryStore, telegram NotificationTelegram) *DeliveryHandler {
	return &DeliveryHandler{store: store, telegram: telegram}
}

func (h *DeliveryHandler) Deliver(w http.ResponseWriter, r *http.Request) {
	deliveryID := strings.TrimSpace(r.PathValue("delivery_id"))
	if !deliveryUUIDPattern.MatchString(deliveryID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_delivery_id", "delivery id is invalid")
		return
	}
	var request struct {
		TelegramChatID   int64  `json:"telegram_chat_id"`
		NotificationType string `json:"notification_type"`
		TemplateVersion  int    `json:"template_version"`
		Text             string `json:"text"`
	}
	if err := decodeDeliveryJSON(r.Body, &request); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if request.TelegramChatID <= 0 || request.TemplateVersion != 1 || utf8.RuneCountInString(request.Text) < 1 || utf8.RuneCountInString(request.Text) > 4096 {
		writeDelivery(w, http.StatusUnprocessableEntity, "permanent", "invalid_notification", 0)
		return
	}
	if _, ok := notificationTypes[request.NotificationType]; !ok || strings.Contains(strings.ToLower(request.Text), "vless://") || strings.Contains(strings.ToLower(request.Text), "/s/") {
		writeDelivery(w, http.StatusUnprocessableEntity, "permanent", "invalid_notification", 0)
		return
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(request.TelegramChatID, 10) + "\n" + request.NotificationType + "\n" + strconv.Itoa(request.TemplateVersion) + "\n" + request.Text))
	requestHash := hex.EncodeToString(sum[:])
	leaseToken, err := cryptoutil.RandomUUID()
	if err != nil {
		writeDelivery(w, http.StatusServiceUnavailable, "retryable", "delivery_guard_unavailable", 0)
		return
	}
	state, err := h.store.StartDelivery(r.Context(), deliveryID, requestHash, leaseToken)
	if err != nil {
		writeDelivery(w, http.StatusServiceUnavailable, "retryable", "delivery_guard_unavailable", 0)
		return
	}
	switch state {
	case "completed":
		writeDelivery(w, http.StatusOK, "replay", "", 0)
		return
	case "processing":
		writeDelivery(w, http.StatusConflict, "retryable", "delivery_in_progress", 1)
		return
	case "conflict":
		writeDelivery(w, http.StatusConflict, "permanent", "delivery_id_conflict", 0)
		return
	case "acquired":
	default:
		writeDelivery(w, http.StatusServiceUnavailable, "retryable", "delivery_guard_unavailable", 0)
		return
	}
	if err := h.telegram.SendHTMLMessage(r.Context(), request.TelegramChatID, request.Text); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		_ = h.store.ReleaseDelivery(cleanupCtx, deliveryID, requestHash, leaseToken)
		cancel()
		status, result, reason, retryAfter := classifyTelegramDelivery(err)
		writeDelivery(w, status, result, reason, retryAfter)
		return
	}
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	err = h.store.CompleteDelivery(completionCtx, deliveryID, requestHash, leaseToken)
	cancel()
	if err != nil {
		writeDelivery(w, http.StatusServiceUnavailable, "retryable", "delivery_result_unavailable", 0)
		return
	}
	writeDelivery(w, http.StatusOK, "delivered", "", 0)
}

func classifyTelegramDelivery(err error) (int, string, string, int) {
	var apiErr *telegramapi.APIError
	if !errors.As(err, &apiErr) {
		return http.StatusServiceUnavailable, "retryable", "telegram_unknown", 0
	}
	if apiErr.Kind == telegramapi.ErrorKindRateLimited {
		return http.StatusServiceUnavailable, "retryable", "telegram_rate_limited", int(apiErr.RetryAfter.Seconds())
	}
	if apiErr.Kind == telegramapi.ErrorKindNetwork || apiErr.Kind == telegramapi.ErrorKindResponseRead || apiErr.Kind == telegramapi.ErrorKindResponseTooLarge || apiErr.Kind == telegramapi.ErrorKindInvalidResponse || apiErr.StatusCode >= 500 {
		return http.StatusServiceUnavailable, "retryable", "telegram_temporary", 0
	}
	if apiErr.StatusCode == http.StatusUnauthorized || apiErr.ErrorCode == http.StatusUnauthorized {
		return http.StatusUnprocessableEntity, "permanent", "telegram_unauthorized", 0
	}
	if apiErr.StatusCode == http.StatusForbidden || apiErr.ErrorCode == http.StatusForbidden {
		return http.StatusUnprocessableEntity, "permanent", "telegram_bot_blocked", 0
	}
	if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 || apiErr.ErrorCode >= 400 && apiErr.ErrorCode < 500 {
		return http.StatusUnprocessableEntity, "permanent", "telegram_invalid_target", 0
	}
	return http.StatusServiceUnavailable, "retryable", "telegram_unknown", 0
}

func decodeDeliveryJSON(body io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxDeliveryBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxDeliveryBodyBytes {
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

func writeDelivery(w http.ResponseWriter, status int, result, reason string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := struct {
		Status            string `json:"status"`
		ReasonCode        string `json:"reason_code,omitempty"`
		RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	}{result, reason, retryAfter}
	_ = json.NewEncoder(w).Encode(payload)
}
