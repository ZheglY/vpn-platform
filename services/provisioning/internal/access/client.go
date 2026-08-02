package access

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
		return nil, fmt.Errorf("invalid access service URL")
	}
	return &Client{baseURL: parsed, http: client}, nil
}

func (c *Client) GetCredentialMaterial(ctx context.Context, credentialID string) (domain.CredentialMaterial, error) {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "internal/v1/credentials/" + url.PathEscape(credentialID) + "/provisioning-material"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return domain.CredentialMaterial{}, fmt.Errorf("create access material request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.CredentialMaterial{}, fmt.Errorf("access service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return domain.CredentialMaterial{}, domain.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return domain.CredentialMaterial{}, fmt.Errorf("access service returned status %d", resp.StatusCode)
	}
	var material domain.CredentialMaterial
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&material); err != nil {
		return domain.CredentialMaterial{}, fmt.Errorf("decode access material response")
	}
	return material, nil
}

func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "livez"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("access service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("access service unavailable")
	}
	return nil
}
