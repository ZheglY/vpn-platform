package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid identity service URL")
	}
	return &Client{baseURL: parsed, http: httpClient}, nil
}

func (c *Client) VerifyActiveWithConsent(ctx context.Context, userID, termsVersion string) error {
	var user struct {
		Status string `json:"status"`
	}
	status, err := c.get(ctx, "internal/v1/users/"+url.PathEscape(userID), &user)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || user.Status != "active" {
		return domain.ErrUserUnavailable
	}
	var consent struct {
		Accepted bool `json:"accepted"`
	}
	path := "internal/v1/users/" + url.PathEscape(userID) + "/consents/terms/" + url.PathEscape(termsVersion)
	status, err = c.get(ctx, path, &consent)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || !consent.Accepted {
		return domain.ErrConsentRequired
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	status, err := c.get(ctx, "livez", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("identity service unavailable")
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, output any) (int, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("create identity request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("identity service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("identity service returned status %d", resp.StatusCode)
	}
	if output == nil {
		return resp.StatusCode, nil
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if err := decoder.Decode(output); err != nil {
		return resp.StatusCode, fmt.Errorf("decode identity response")
	}
	return resp.StatusCode, nil
}
