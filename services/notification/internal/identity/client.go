package identity

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
	"github.com/ZheglY/vpn-platform/services/notification/internal/httpdecode"
)

type Client struct {
	baseURL         *url.URL
	http            *http.Client
	documentType    string
	documentVersion string
}

func NewClient(baseURL, documentType, documentVersion string, client *http.Client) (*Client, error) {
	parsed, err := fixedBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("identity URL: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("identity HTTP client is required")
	}
	if strings.TrimSpace(documentType) == "" || strings.TrimSpace(documentVersion) == "" || len(documentType) > 64 || len(documentVersion) > 64 {
		return nil, fmt.Errorf("consent document identity is invalid")
	}
	return &Client{baseURL: parsed, http: client, documentType: documentType, documentVersion: documentVersion}, nil
}

func (c *Client) GetNotificationTarget(ctx context.Context, userID string) (domain.TelegramTarget, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/users/" + url.PathEscape(userID) + "/notification-target"})
	query := endpoint.Query()
	query.Set("document_type", c.documentType)
	query.Set("document_version", c.documentVersion)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.TelegramTarget{}, fmt.Errorf("create identity target request: %w", err)
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.TelegramTarget{}, fmt.Errorf("identity target unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.TelegramTarget{}, fmt.Errorf("identity target status %d", resp.StatusCode)
	}
	var payload struct {
		Eligible       bool    `json:"eligible"`
		ReasonCode     string  `json:"reason_code"`
		TelegramChatID *int64  `json:"telegram_chat_id"`
		Locale         *string `json:"locale"`
	}
	if err := httpdecode.Strict(resp.Body, 16<<10, &payload); err != nil {
		return domain.TelegramTarget{}, fmt.Errorf("decode identity target: %w", err)
	}
	if payload.Eligible && (payload.TelegramChatID == nil || *payload.TelegramChatID <= 0 || payload.ReasonCode != "") {
		return domain.TelegramTarget{}, fmt.Errorf("identity target invariant failed")
	}
	if !payload.Eligible && (payload.TelegramChatID != nil || !boundedToken(payload.ReasonCode, 3, 64)) {
		return domain.TelegramTarget{}, fmt.Errorf("identity target invariant failed")
	}
	locale := ""
	if payload.Locale != nil {
		if !boundedToken(*payload.Locale, 2, 16) {
			return domain.TelegramTarget{}, fmt.Errorf("identity target locale is invalid")
		}
		locale = *payload.Locale
	}
	var chatID int64
	if payload.TelegramChatID != nil {
		chatID = *payload.TelegramChatID
	}
	return domain.TelegramTarget{Eligible: payload.Eligible, ReasonCode: payload.ReasonCode, TelegramChatID: chatID, Locale: locale}, nil
}

func boundedToken(value string, minLength, maxLength int) bool {
	if len(value) < minLength || len(value) > maxLength {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func fixedBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("must be an absolute fixed URL")
	}
	return parsed, nil
}
