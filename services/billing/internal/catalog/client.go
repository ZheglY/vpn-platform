package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
		return nil, fmt.Errorf("invalid catalog service URL")
	}
	return &Client{baseURL: parsed, http: httpClient}, nil
}

func (c *Client) GetPlan(ctx context.Context, planID, region string) (domain.PlanSnapshot, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/plans/" + url.PathEscape(planID)})
	query := endpoint.Query()
	query.Set("region", region)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.PlanSnapshot{}, fmt.Errorf("create catalog request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.PlanSnapshot{}, fmt.Errorf("catalog service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return domain.PlanSnapshot{}, domain.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.PlanSnapshot{}, fmt.Errorf("catalog service returned status %d", resp.StatusCode)
	}
	var plan struct {
		PlanID           string   `json:"plan_id"`
		Name             string   `json:"name"`
		DurationDays     int      `json:"duration_days"`
		GracePeriodHours int      `json:"grace_period_hours"`
		AmountMinor      int64    `json:"amount_minor"`
		Currency         string   `json:"currency"`
		RegionPolicy     string   `json:"region_policy"`
		TrafficPolicy    string   `json:"traffic_policy"`
		PrimaryNodes     int      `json:"primary_nodes"`
		FailoverNodes    int      `json:"failover_nodes"`
		Regions          []string `json:"regions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&plan); err != nil {
		return domain.PlanSnapshot{}, fmt.Errorf("decode catalog response")
	}
	if !slices.Contains(plan.Regions, region) {
		return domain.PlanSnapshot{}, domain.ErrNotFound
	}
	return domain.PlanSnapshot{PlanID: plan.PlanID, Name: plan.Name, DurationDays: plan.DurationDays, GracePeriodHours: plan.GracePeriodHours, AmountMinor: plan.AmountMinor, Currency: plan.Currency, Region: region, RegionPolicy: plan.RegionPolicy, TrafficPolicy: plan.TrafficPolicy, PrimaryNodes: plan.PrimaryNodes, FailoverNodes: plan.FailoverNodes}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "livez"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("catalog service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog service unavailable")
	}
	return nil
}
