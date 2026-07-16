package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	apiBaseURL *url.URL
	token      string
	httpClient *http.Client
}

func NewClient(apiBaseURL, token string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(apiBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Telegram API base URL: %w", err)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		apiBaseURL: parsed,
		token:      token,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	endpoint := c.apiBaseURL.ResolveReference(&url.URL{Path: "/bot" + c.token + "/sendMessage"})
	payload, err := json.Marshal(map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("marshal Telegram sendMessage request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Telegram sendMessage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call Telegram sendMessage: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage returned status %d", resp.StatusCode)
	}
	return nil
}
