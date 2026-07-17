package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/telegram-bot/internal/bot"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid billing URL")
	}
	return &Client{baseURL: parsed, http: httpClient}, nil
}
func (c *Client) CreateOrder(ctx context.Context, userID, planID, region, termsVersion, idempotencyKey string) (bot.Order, error) {
	var order bot.Order
	err := c.post(ctx, "internal/v1/users/"+url.PathEscape(userID)+"/orders", idempotencyKey, map[string]string{"plan_id": planID, "region": region, "accepted_terms_version": termsVersion}, &order)
	return order, err
}
func (c *Client) CreatePayment(ctx context.Context, userID, orderID, idempotencyKey string) (bot.Payment, error) {
	var payment bot.Payment
	err := c.post(ctx, "internal/v1/users/"+url.PathEscape(userID)+"/orders/"+url.PathEscape(orderID)+"/payments", idempotencyKey, nil, &payment)
	return payment, err
}
func (c *Client) post(ctx context.Context, path, idempotencyKey string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode billing request")
		}
		body = bytes.NewReader(payload)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create billing request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("billing unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("billing returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(output); err != nil {
		return fmt.Errorf("decode billing response")
	}
	return nil
}
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "livez"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("billing unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("billing unavailable")
	}
	return nil
}
