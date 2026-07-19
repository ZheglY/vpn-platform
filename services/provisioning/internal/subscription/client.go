package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(baseURL string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid subscription service URL")
	}
	return &Client{baseURL: parsed, http: client}, nil
}

func (c *Client) GetPlacement(ctx context.Context, subscriptionID string) (domain.Placement, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/subscriptions/" + url.PathEscape(subscriptionID) + "/placement"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.Placement{}, fmt.Errorf("create subscription placement request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.Placement{}, fmt.Errorf("subscription service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return domain.Placement{}, domain.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Placement{}, fmt.Errorf("subscription service returned status %d", resp.StatusCode)
	}
	var placement domain.Placement
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&placement); err != nil {
		return domain.Placement{}, fmt.Errorf("decode subscription placement response")
	}
	return placement, nil
}

func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "livez"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("subscription service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("subscription service unavailable")
	}
	return nil
}
