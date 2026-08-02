package bot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
)

const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

type WebhookHandler struct {
	secret         string
	consentVersion string
	termsURL       string
	identity       IdentityClient
	catalog        CatalogClient
	billing        BillingClient
	telegram       TelegramClient
	dedupe         DedupeStore
	fsm            FSMStore
	rateLimiter    RateLimiter
	rateLimitKey   string
	logger         *zap.Logger
	cleanupTimeout time.Duration
}

type Config struct {
	WebhookSecret  string
	ConsentVersion string
	TermsURL       string
	CleanupTimeout time.Duration
	RateLimitKey   string
}

func NewWebhookHandler(cfg Config, identity IdentityClient, telegram TelegramClient, dedupe DedupeStore, fsm FSMStore, rateLimiter RateLimiter, logger *zap.Logger) *WebhookHandler {
	return NewWebhookHandlerWithCommerce(cfg, identity, nil, nil, telegram, dedupe, fsm, rateLimiter, logger)
}

func NewWebhookHandlerWithCommerce(cfg Config, identity IdentityClient, catalog CatalogClient, billing BillingClient, telegram TelegramClient, dedupe DedupeStore, fsm FSMStore, rateLimiter RateLimiter, logger *zap.Logger) *WebhookHandler {
	rateLimitKey := cfg.RateLimitKey
	if rateLimitKey == "" {
		rateLimitKey = "telegram-webhook"
	}
	return &WebhookHandler{
		secret:         cfg.WebhookSecret,
		consentVersion: cfg.ConsentVersion,
		termsURL:       cfg.TermsURL,
		identity:       identity,
		catalog:        catalog,
		billing:        billing,
		telegram:       telegram,
		dedupe:         dedupe,
		fsm:            fsm,
		rateLimiter:    rateLimiter,
		rateLimitKey:   rateLimitKey,
		logger:         logger,
		cleanupTimeout: cfg.CleanupTimeout,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.validSecret(r.Header.Get(telegramSecretHeader)) {
		httperror.Write(w, r, http.StatusUnauthorized, "unauthenticated", "invalid Telegram webhook secret")
		return
	}
	if h.rateLimiter != nil {
		allowed, err := h.rateLimiter.Allow(r.Context(), h.rateLimitKey)
		if err != nil {
			httperror.Write(w, r, http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limit is unavailable")
			return
		}
		if !allowed {
			httperror.Write(w, r, http.StatusTooManyRequests, "rate_limited", "too many Telegram webhook requests")
			return
		}
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

	token := fmt.Sprintf("%d:%d", update.UpdateID, time.Now().UnixNano())
	status, err := h.dedupe.StartProcessing(r.Context(), update.UpdateID, token)
	if err != nil {
		httperror.Write(w, r, http.StatusServiceUnavailable, "dedupe_unavailable", "update dedupe is unavailable")
		return
	}
	if status == DedupeCompleted {
		w.WriteHeader(http.StatusOK)
		return
	}
	if status == DedupeProcessing {
		httperror.Write(w, r, http.StatusServiceUnavailable, "update_processing", "update is already being processed")
		return
	}

	if err := h.process(r, update); err != nil {
		cleanupCtx, cancel := h.cleanupContext(r.Context())
		defer cancel()
		if forgetErr := h.dedupe.ReleaseProcessing(cleanupCtx, update.UpdateID, token); forgetErr != nil {
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

	cleanupCtx, cancel := h.cleanupContext(r.Context())
	defer cancel()
	if err := h.dedupe.CompleteProcessing(cleanupCtx, update.UpdateID, token); err != nil {
		h.logger.Warn("telegram update dedupe completion failed",
			zap.Int64("update_id", update.UpdateID),
			zap.String("error_type", fmt.Sprintf("%T", err)),
		)
		httperror.Write(w, r, http.StatusServiceUnavailable, "dedupe_unavailable", "update dedupe is unavailable")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) cleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := h.cleanupTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
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
	if user.Status != "active" {
		return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, unavailableText())
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
			return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, consentPrompt(h.consentVersion, h.termsURL))
		}
		if err := h.fsm.SetState(r.Context(), profile.TelegramUserID, StateMenu); err != nil {
			return err
		}
		return h.telegram.SendMessage(r.Context(), update.Message.Chat.ID, mainMenuText())
	case "/plans", "plans":
		accepted, err := h.ensureConsent(r.Context(), user.UserID, profile.TelegramUserID, update.Message.Chat.ID)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		return h.sendPlans(r.Context(), update.Message.Chat.ID)
	case "/buy", "buy":
		accepted, err := h.ensureConsent(r.Context(), user.UserID, profile.TelegramUserID, update.Message.Chat.ID)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		return h.createPayment(r.Context(), update, user.UserID)
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

func (h *WebhookHandler) ensureConsent(ctx context.Context, userID string, telegramUserID, chatID int64) (bool, error) {
	accepted, err := h.identity.HasConsent(ctx, userID, "terms", h.consentVersion)
	if err != nil {
		return false, err
	}
	if !accepted {
		if err := h.fsm.SetState(ctx, telegramUserID, StateAwaitingConsent); err != nil {
			return false, err
		}
		if err := h.telegram.SendMessage(ctx, chatID, consentPrompt(h.consentVersion, h.termsURL)); err != nil {
			return false, err
		}
	}
	return accepted, nil
}

func (h *WebhookHandler) sendPlans(ctx context.Context, chatID int64) error {
	if h.catalog == nil {
		return h.telegram.SendMessage(ctx, chatID, "Plans are temporarily unavailable.")
	}
	plans, err := h.catalog.ListPlans(ctx)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		return h.telegram.SendMessage(ctx, chatID, "No plans are currently available.")
	}
	plan := plans[0]
	return h.telegram.SendMessage(ctx, chatID, formatPlan(plan)+"\nSend /buy to create a sandbox payment.")
}

func (h *WebhookHandler) createPayment(ctx context.Context, update Update, userID string) error {
	if h.catalog == nil || h.billing == nil {
		return h.telegram.SendMessage(ctx, update.Message.Chat.ID, "Payments are temporarily unavailable.")
	}
	plans, err := h.catalog.ListPlans(ctx)
	if err != nil {
		return err
	}
	if len(plans) == 0 || len(plans[0].Regions) == 0 {
		return h.telegram.SendMessage(ctx, update.Message.Chat.ID, "No plans are currently available.")
	}
	plan := plans[0]
	orderKey := fmt.Sprintf("tg:%d:order", update.UpdateID)
	order, err := h.billing.CreateOrder(ctx, userID, plan.PlanID, plan.Regions[0], h.consentVersion, orderKey)
	if err != nil {
		return err
	}
	paymentKey := fmt.Sprintf("tg:%d:payment", update.UpdateID)
	payment, err := h.billing.CreatePayment(ctx, userID, order.OrderID, paymentKey)
	if err != nil {
		return err
	}
	if payment.ConfirmationURL == nil {
		return h.telegram.SendMessage(ctx, update.Message.Chat.ID, "Payment creation is being reconciled. Send /buy again shortly to check it.")
	}
	return h.telegram.SendMessage(ctx, update.Message.Chat.ID, "Complete the sandbox payment: "+*payment.ConfirmationURL)
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

func consentPrompt(version, termsURL string) string {
	return "Please review terms version " + version + ": " + termsURL + "\nTo accept, send: accept"
}

func mainMenuText() string {
	return "Menu: /plans, /buy, subscription status, help."
}

func formatPlan(plan Plan) string {
	major := plan.AmountMinor / 100
	minor := plan.AmountMinor % 100
	return fmt.Sprintf("%s: %d days, %d.%02d %s, regions: %s", plan.Name, plan.DurationDays, major, minor, plan.Currency, strings.Join(plan.Regions, ", "))
}

func unavailableText() string {
	return "Account is unavailable. Contact support."
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
