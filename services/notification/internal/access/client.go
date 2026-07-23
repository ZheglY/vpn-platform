package access

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
		return nil, fmt.Errorf("access URL must be absolute and fixed")
	}
	if client == nil {
		return nil, fmt.Errorf("access HTTP client is required")
	}
	return &Client{baseURL: parsed, http: client}, nil
}

func (c *Client) IsCurrentReady(ctx context.Context, subscriptionID, credentialID string) (bool, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/subscriptions/" + url.PathEscape(subscriptionID) + "/access"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, fmt.Errorf("create access status request: %w", err)
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("access status unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("access status %d", resp.StatusCode)
	}
	var payload struct {
		SubscriptionID     string `json:"subscription_id"`
		CredentialID       string `json:"credential_id"`
		AccessStatus       string `json:"access_status"`
		ProvisioningStatus string `json:"provisioning_status"`
		TokenStatus        string `json:"token_status"`
	}
	if err := httpdecode.Strict(resp.Body, 16<<10, &payload); err != nil {
		return false, fmt.Errorf("decode access status: %w", err)
	}
	if payload.SubscriptionID != subscriptionID || payload.CredentialID == "" || payload.AccessStatus == "" || payload.ProvisioningStatus == "" || payload.TokenStatus == "" {
		return false, fmt.Errorf("access status invariant failed")
	}
	return payload.CredentialID == credentialID && (payload.AccessStatus == "ready" || payload.AccessStatus == "active") && (payload.ProvisioningStatus == "active" || payload.ProvisioningStatus == "degraded"), nil
}
