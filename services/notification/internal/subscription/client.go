package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/notification/internal/httpdecode"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(baseURL string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("subscription URL must be absolute and fixed")
	}
	if client == nil {
		return nil, fmt.Errorf("subscription HTTP client is required")
	}
	return &Client{baseURL: parsed, http: client}, nil
}

func (c *Client) IsEntitled(ctx context.Context, userID, subscriptionID string) (bool, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/users/" + url.PathEscape(userID) + "/subscription"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, fmt.Errorf("create subscription status request: %w", err)
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("subscription status unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("subscription status %d", resp.StatusCode)
	}
	var payload struct {
		SubscriptionID     string     `json:"subscription_id"`
		UserID             string     `json:"user_id"`
		Status             string     `json:"status"`
		CurrentPeriodStart *time.Time `json:"current_period_start"`
		CurrentPeriodEnd   *time.Time `json:"current_period_end"`
		GraceEndsAt        *time.Time `json:"grace_ends_at"`
	}
	if err := httpdecode.Strict(resp.Body, 16<<10, &payload); err != nil {
		return false, fmt.Errorf("decode subscription status: %w", err)
	}
	if payload.UserID != userID || payload.SubscriptionID != subscriptionID {
		return false, fmt.Errorf("subscription status invariant failed")
	}
	switch payload.Status {
	case "active", "grace":
		return true, nil
	case "pending", "expired", "revoked":
		return false, nil
	default:
		return false, fmt.Errorf("subscription status invariant failed")
	}
}
