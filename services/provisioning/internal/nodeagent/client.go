package nodeagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

type Client struct {
	baseTransport *http.Transport
	timeout       time.Duration
}

func NewClient(base *http.Client, timeout time.Duration) (*Client, error) {
	transport, ok := base.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		return nil, fmt.Errorf("node-agent client requires a TLS transport")
	}
	return &Client{baseTransport: transport.Clone(), timeout: timeout}, nil
}

func (c *Client) Apply(ctx context.Context, node domain.Node, desired domain.AgentDesiredState) (domain.AgentResult, error) {
	body, err := json.Marshal(desired)
	if err != nil {
		return domain.AgentResult{}, fmt.Errorf("encode node desired state")
	}
	var result domain.AgentResult
	if err := c.doJSON(ctx, node, http.MethodPut, "internal/v1/credentials/"+url.PathEscape(desired.CredentialID), bytes.NewReader(body), &result); err != nil {
		return domain.AgentResult{}, err
	}
	return result, nil
}

func (c *Client) Status(ctx context.Context, node domain.Node) (domain.AgentStatus, error) {
	var status domain.AgentStatus
	if err := c.doJSON(ctx, node, http.MethodGet, "internal/v1/status", nil, &status); err != nil {
		return domain.AgentStatus{}, err
	}
	return status, nil
}

func (c *Client) CredentialState(ctx context.Context, node domain.Node, credentialID string) (domain.CredentialActualState, error) {
	var state domain.CredentialActualState
	if err := c.doJSON(ctx, node, http.MethodGet, "internal/v1/credentials/"+url.PathEscape(credentialID), nil, &state); err != nil {
		return domain.CredentialActualState{}, err
	}
	return state, nil
}

func (c *Client) doJSON(ctx context.Context, node domain.Node, method, path string, body io.Reader, target any) error {
	base, err := url.Parse(strings.TrimRight(node.ManagementURL, "/") + "/")
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return fmt.Errorf("invalid node management URL")
	}
	endpoint := base.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create node-agent request")
	}
	req.Header.Set(requestid.Header, requestid.New())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.clientForNode(node.ManagementSPIFFEID)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("node-agent unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return domain.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("node-agent rejected request with status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode node-agent response")
	}
	return nil
}

func (c *Client) clientForNode(expectedSPIFFEID string) *http.Client {
	transport := c.baseTransport.Clone()
	tlsConfig := transport.TLSClientConfig.Clone()
	previousVerify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if previousVerify != nil {
			if err := previousVerify(state); err != nil {
				return err
			}
		}
		if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
			return errors.New("node certificate chain was not verified")
		}
		uris := state.VerifiedChains[0][0].URIs
		if !slices.ContainsFunc(uris, func(uri *url.URL) bool { return uri != nil && uri.String() == expectedSPIFFEID }) {
			return errors.New("node certificate identity mismatch")
		}
		return nil
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: c.timeout}
}
