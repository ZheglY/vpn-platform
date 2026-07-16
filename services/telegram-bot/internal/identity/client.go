package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/yarik/vpn-service/internal/platform/requestid"
	"github.com/yarik/vpn-service/services/telegram-bot/internal/bot"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewClient(baseURL string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return NewClientWithHTTPClient(baseURL, &http.Client{Timeout: timeout})
}

func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse identity base URL: %w", err)
	}
	if httpClient == nil {
		return nil, fmt.Errorf("identity HTTP client is required")
	}
	return &Client{
		baseURL:    parsed,
		httpClient: httpClient,
	}, nil
}

func (c *Client) UpsertTelegramIdentity(ctx context.Context, profile bot.TelegramProfile) (bot.IdentityUser, error) {
	path := fmt.Sprintf("/internal/v1/telegram-users/%d", profile.TelegramUserID)
	reqBody := map[string]*string{
		"username":      profile.Username,
		"display_name":  profile.DisplayName,
		"language_code": profile.LanguageCode,
		"locale":        profile.Locale,
	}
	var user bot.IdentityUser
	if err := c.doJSON(ctx, http.MethodPut, path, reqBody, &user); err != nil {
		return bot.IdentityUser{}, err
	}
	return user, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodGet, "/livez", nil, nil)
}

func (c *Client) HasConsent(ctx context.Context, userID, documentType, documentVersion string) (bool, error) {
	path := fmt.Sprintf("/internal/v1/users/%s/consents/%s/%s", url.PathEscape(userID), url.PathEscape(documentType), url.PathEscape(documentVersion))
	var response struct {
		Accepted bool `json:"accepted"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return false, err
	}
	return response.Accepted, nil
}

func (c *Client) AcceptConsent(ctx context.Context, userID, documentType, documentVersion string) error {
	path := fmt.Sprintf("/internal/v1/users/%s/consents", url.PathEscape(userID))
	reqBody := map[string]string{
		"document_type":    documentType,
		"document_version": documentVersion,
	}
	return c.doJSON(ctx, http.MethodPost, path, reqBody, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	var body *bytes.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal identity request: %w", err)
		}
		body = bytes.NewReader(payload)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create identity request: %w", err)
	}
	req.Header.Set("X-Request-Id", requestid.New())
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call identity service: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("identity service returned status %d", resp.StatusCode)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode identity response: %w", err)
	}
	return nil
}
