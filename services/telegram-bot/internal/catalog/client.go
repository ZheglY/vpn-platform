package catalog

import (
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
		return nil, fmt.Errorf("invalid catalog URL")
	}
	return &Client{baseURL: parsed, http: httpClient}, nil
}
func (c *Client) ListPlans(ctx context.Context) ([]bot.Plan, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "v1/plans", RawQuery: "channel=telegram"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create catalog request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned status %d", resp.StatusCode)
	}
	var response struct {
		Plans []bot.Plan `json:"plans"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode catalog response")
	}
	return response.Plans, nil
}
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "livez"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("catalog unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog unavailable")
	}
	return nil
}
