package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxResponseBytes = 64 * 1024

const (
	ErrorKindNetwork          = "network_error"
	ErrorKindResponseRead     = "response_read_error"
	ErrorKindResponseTooLarge = "response_too_large"
	ErrorKindInvalidResponse  = "invalid_response"
	ErrorKindHTTPStatus       = "http_status"
	ErrorKindAPI              = "api_error"
	ErrorKindRateLimited      = "rate_limited"
)

type Client struct {
	apiBaseURL *url.URL
	token      string
	httpClient *http.Client
}

func NewClient(apiBaseURL, token string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return NewClientWithHTTPClient(apiBaseURL, token, &http.Client{Timeout: timeout})
}

func NewClientWithHTTPClient(apiBaseURL, token string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(apiBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Telegram API base URL: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("telegram bot token is required")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("telegram HTTP client is required")
	}
	return &Client{
		apiBaseURL: parsed,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	endpoint := c.apiBaseURL.ResolveReference(&url.URL{Path: "/bot" + c.token + "/sendMessage"})
	payload, err := json.Marshal(sendMessageRequest{
		ChatID: chatID,
		Text:   text,
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
		return &APIError{Kind: ErrorKindNetwork}
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	envelope, err := decodeEnvelope(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return telegramAPIError(ErrorKindHTTPStatus, resp.StatusCode, envelope)
	}
	if !envelope.OK {
		return telegramAPIError(ErrorKindAPI, resp.StatusCode, envelope)
	}
	return nil
}

type responseEnvelope struct {
	OK          bool                `json:"ok"`
	Result      json.RawMessage     `json:"result,omitempty"`
	ErrorCode   int                 `json:"error_code,omitempty"`
	Description string              `json:"description,omitempty"`
	Parameters  *responseParameters `json:"parameters,omitempty"`
}

type sendMessageRequest struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

type responseParameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type APIError struct {
	Kind       string
	StatusCode int
	ErrorCode  int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram api request failed: %s retry_after=%s", e.Kind, e.RetryAfter)
	}
	return fmt.Sprintf("telegram api request failed: %s", e.Kind)
}

func decodeEnvelope(body io.Reader) (responseEnvelope, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return responseEnvelope{}, &APIError{Kind: ErrorKindResponseRead}
	}
	if len(payload) > maxResponseBytes {
		return responseEnvelope{}, &APIError{Kind: ErrorKindResponseTooLarge}
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return responseEnvelope{}, &APIError{Kind: ErrorKindInvalidResponse}
	}
	return envelope, nil
}

func telegramAPIError(kind string, statusCode int, envelope responseEnvelope) *APIError {
	retryAfter := time.Duration(0)
	if envelope.Parameters != nil && envelope.Parameters.RetryAfter > 0 {
		retryAfter = time.Duration(envelope.Parameters.RetryAfter) * time.Second
		kind = ErrorKindRateLimited
	}
	return &APIError{
		Kind:       kind,
		StatusCode: statusCode,
		ErrorCode:  envelope.ErrorCode,
		RetryAfter: retryAfter,
	}
}
