package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
	"github.com/ZheglY/vpn-platform/services/notification/internal/httpdecode"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(baseURL string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("telegram-bot URL must be absolute and fixed")
	}
	if client == nil {
		return nil, fmt.Errorf("telegram-bot HTTP client is required")
	}
	return &Client{baseURL: parsed, http: client}, nil
}

func (c *Client) Deliver(ctx context.Context, deliveryID string, chatID int64, notificationType string, templateVersion int, text string) (domain.DeliveryResult, error) {
	payload, err := json.Marshal(map[string]any{
		"telegram_chat_id": chatID, "notification_type": notificationType,
		"template_version": templateVersion, "text": text,
	})
	if err != nil {
		return domain.DeliveryResult{}, fmt.Errorf("encode Telegram delivery request: %w", err)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/notifications/" + url.PathEscape(deliveryID) + "/telegram"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return domain.DeliveryResult{}, fmt.Errorf("create Telegram delivery request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.DeliveryResult{}, &domain.DeliveryError{Code: "telegram_network", Retryable: true}
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Status            string `json:"status"`
		ReasonCode        string `json:"reason_code"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	if err := httpdecode.Strict(resp.Body, 16<<10, &result); err != nil {
		return domain.DeliveryResult{}, &domain.DeliveryError{Code: "telegram_invalid_response", Retryable: true}
	}
	if result.RetryAfterSeconds < 0 || result.RetryAfterSeconds > 900 {
		return domain.DeliveryResult{}, &domain.DeliveryError{Code: "telegram_invalid_response", Retryable: true}
	}
	switch result.Status {
	case "delivered":
		if resp.StatusCode != http.StatusOK {
			return invalidResponse()
		}
		return domain.DeliveryResult{}, nil
	case "replay":
		if resp.StatusCode != http.StatusOK {
			return invalidResponse()
		}
		return domain.DeliveryResult{Replay: true}, nil
	case "retryable":
		if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusServiceUnavailable {
			return invalidResponse()
		}
		return domain.DeliveryResult{}, &domain.DeliveryError{Code: boundedCode(result.ReasonCode, "telegram_retryable"), Retryable: true, RetryAfter: time.Duration(result.RetryAfterSeconds) * time.Second}
	case "permanent":
		if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusUnprocessableEntity {
			return invalidResponse()
		}
		return domain.DeliveryResult{}, &domain.DeliveryError{Code: boundedCode(result.ReasonCode, "telegram_permanent"), Retryable: false}
	default:
		return invalidResponse()
	}
}

func invalidResponse() (domain.DeliveryResult, error) {
	return domain.DeliveryResult{}, &domain.DeliveryError{Code: "telegram_invalid_response", Retryable: true}
}

func boundedCode(value, fallback string) string {
	if len(value) < 3 || len(value) > 64 {
		return fallback
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return fallback
	}
	return value
}
