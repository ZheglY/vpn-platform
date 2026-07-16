package bot

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/yarik/vpn-service/internal/platform/httperror"
)

const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

type WebhookHandler struct {
	secret         string
	consentVersion string
	identity       IdentityClient
	telegram       TelegramClient
	dedupe         DedupeStore
	fsm            FSMStore
	logger         *zap.Logger
}

type Config struct {
	WebhookSecret  string
	ConsentVersion string
}

func NewWebhookHandler(cfg Config, identity IdentityClient, telegram TelegramClient, dedupe DedupeStore, fsm FSMStore, logger *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		secret:         cfg.WebhookSecret,
		consentVersion: cfg.ConsentVersion,
		identity:       identity,
		telegram:       telegram,
		dedupe:         dedupe,
		fsm:            fsm,
		logger:         logger,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.validSecret(r.Header.Get(telegramSecretHeader)) {
		httperror.Write(w, r, http.StatusUnauthorized, "unauthenticated", "invalid Telegram webhook secret")
		return
	}

	var update Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if update.UpdateID <= 0 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_update", "update_id is required")
		return
	}

	firstSeen, err := h.dedupe.MarkProcessed(r.Context(), update.UpdateID)
	if err != nil {
		httperror.Write(w, r, http.StatusServiceUnavailable, "dedupe_unavailable", "update dedupe is unavailable")
		return
	}
	if !firstSeen {
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.process(r, update); err != nil {
		if forgetErr := h.dedupe.ForgetProcessed(r.Context(), update.UpdateID); forgetErr != nil {
			h.logger.Warn("telegram update dedupe release failed",
				zap.Int64("update_id", update.UpdateID),
				zap.String("error_type", fmt.Sprintf("%T", forgetErr)),
			)
		}
		h.logger.Warn("telegram update processing failed",
			zap.Int64("update_id", update.UpdateID),
			zap.String("error_type", fmt.Sprintf("%T", err)),
		)
		httperror.Write(w, r, http.StatusServiceUnavailable, "telegram_update_failed", "update processing failed")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) process(r *http.Request, update Update) error {
	if update.Message == nil || update.Message.From == nil || update.Message.Chat.ID == 0 {
		return nil
	}

	text := strings.TrimSpace(update.Message.Text)
	if text == "" {
		return nil
	}

	profile := TelegramProfile{
		TelegramUserID: update.Message.From.ID,
		Username:       normalize(update.Message.From.Username),
		DisplayName:    displayName(update.Message.From.FirstName, update.Message.From.LastName),
		LanguageCode:   normalize(update.Message.From.LanguageCode),
		Locale:         normalize(update.Message.From.LanguageCode),
	}
	user, err := h.identity.UpsertTelegramIdentity(r.Context(), profile)
	if err != nil {
		return err
	}

	switch normalizedCommand(text) {
	case "/start":
		accepted, err := h.identity.HasConsent(r.Context(), user.UserID, "terms", h.consentVersion)
		if err != nil {
			return err
		}
		if !accepted {
			if err := h.fsm.SetState(r.Context(), profile.TelegramUserID, StateAwaitingConsent); err != nil {
				return err
			}
			return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, consentPrompt(h.consentVersion))
		}
		if err := h.fsm.SetState(r.Context(), profile.TelegramUserID, StateMenu); err != nil {
			return err
		}
		return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, mainMenuText())
	}

	state, err := h.fsm.GetState(r.Context(), profile.TelegramUserID)
	if err != nil {
		return err
	}
	if state == StateAwaitingConsent && isConsentAccept(text) {
		if err := h.identity.AcceptConsent(r.Context(), user.UserID, "terms", h.consentVersion); err != nil {
			return err
		}
		if err := h.fsm.SetState(r.Context(), profile.TelegramUserID, StateMenu); err != nil {
			return err
		}
		return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, mainMenuText())
	}

	return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, "Send /start to open the menu.")
}

func (h *WebhookHandler) validSecret(value string) bool {
	if h.secret == "" || value == "" {
		return false
	}
	if len(value) != len(h.secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(h.secret)) == 1
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text,omitempty"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

func normalizedCommand(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(fields[0])
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	return command
}

func isConsentAccept(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return normalized == "accept" ||
		normalized == "i accept" ||
		normalized == "\u0441\u043e\u0433\u043b\u0430\u0441\u0435\u043d" ||
		normalized == "\u043f\u0440\u0438\u043d\u0438\u043c\u0430\u044e"
}

func consentPrompt(version string) string {
	return "Please accept the current terms version " + version + " by sending: accept"
}

func mainMenuText() string {
	return "Menu: plans, subscription status, help."
}

func normalize(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func displayName(firstName, lastName string) *string {
	value := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if value == "" {
		return nil
	}
	return &value
}
