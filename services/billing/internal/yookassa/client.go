package yookassa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	platformtelemetry "github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

const maxResponseBytes = 64 << 10

type Client struct {
	baseURL   *url.URL
	shopID    string
	secretKey string
	returnURL string
	http      *http.Client
}

func NewClient(baseURL, shopID, secretKey, returnURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid YooKassa base URL")
	}
	if strings.TrimSpace(shopID) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, fmt.Errorf("YooKassa shop id and secret key are required")
	}
	returnParsed, err := url.Parse(returnURL)
	if err != nil || returnParsed.Scheme != "https" || returnParsed.Host == "" || returnParsed.User != nil {
		return nil, fmt.Errorf("invalid payment return URL")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second, Transport: platformtelemetry.WrapHTTPTransport(nil)}
	}
	return &Client{baseURL: parsed, shopID: shopID, secretKey: secretKey, returnURL: returnURL, http: httpClient}, nil
}

func (c *Client) CreatePayment(ctx context.Context, input domain.ProviderCreateRequest) (domain.ProviderPayment, error) {
	payload := createPaymentRequest{
		Amount:       amount{Value: formatMinor(input.AmountMinor), Currency: input.Currency},
		Capture:      true,
		Confirmation: confirmation{Type: "redirect", ReturnURL: c.returnURL},
		Description:  "VPN order " + shortID(input.OrderID),
		Metadata: map[string]string{
			"order_id":   input.OrderID,
			"payment_id": input.PaymentID,
		},
	}
	var response paymentResponse
	if err := c.do(ctx, http.MethodPost, "v3/payments", input.IdempotencyKey, payload, &response, true); err != nil {
		return domain.ProviderPayment{}, err
	}
	return normalizePayment(response)
}

func (c *Client) GetPayment(ctx context.Context, paymentID string) (domain.ProviderPayment, error) {
	if strings.TrimSpace(paymentID) == "" || strings.ContainsAny(paymentID, "/?#") {
		return domain.ProviderPayment{}, &domain.ProviderError{Kind: "permanent", Code: "invalid_payment_id"}
	}
	var response paymentResponse
	if err := c.do(ctx, http.MethodGet, "v3/payments/"+url.PathEscape(paymentID), "", nil, &response, false); err != nil {
		return domain.ProviderPayment{}, err
	}
	return normalizePayment(response)
}

func (c *Client) do(ctx context.Context, method, path, idempotencyKey string, input, output any, ambiguous bool) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return &domain.ProviderError{Kind: "permanent", Code: "encode_request"}
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return &domain.ProviderError{Kind: "permanent", Code: "create_request"}
	}
	req.SetBasicAuth(c.shopID, c.secretKey)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotence-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		kind := "temporary"
		if ambiguous {
			kind = "ambiguous"
		}
		return &domain.ProviderError{Kind: kind, Code: "transport_error"}
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		return &domain.ProviderError{Kind: "temporary", Code: "invalid_response"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := "permanent"
		if resp.StatusCode == http.StatusTooManyRequests {
			kind = "temporary"
		} else if resp.StatusCode >= 500 {
			kind = "temporary"
			if ambiguous {
				kind = "ambiguous"
			}
		}
		return &domain.ProviderError{Kind: kind, Code: "http_" + strconv.Itoa(resp.StatusCode)}
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &domain.ProviderError{Kind: "temporary", Code: "invalid_json"}
	}
	return nil
}

type amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type confirmation struct {
	Type            string `json:"type"`
	ReturnURL       string `json:"return_url,omitempty"`
	ConfirmationURL string `json:"confirmation_url,omitempty"`
}

type createPaymentRequest struct {
	Amount       amount            `json:"amount"`
	Capture      bool              `json:"capture"`
	Confirmation confirmation      `json:"confirmation"`
	Description  string            `json:"description"`
	Metadata     map[string]string `json:"metadata"`
}

type paymentResponse struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	Amount       amount            `json:"amount"`
	Confirmation *confirmation     `json:"confirmation,omitempty"`
	Metadata     map[string]string `json:"metadata"`
	Test         bool              `json:"test"`
	CapturedAt   *time.Time        `json:"captured_at,omitempty"`
	CanceledAt   *time.Time        `json:"canceled_at,omitempty"`
	Recipient    struct {
		AccountID string `json:"account_id"`
	} `json:"recipient"`
}

func normalizePayment(response paymentResponse) (domain.ProviderPayment, error) {
	minor, err := parseMinor(response.Amount.Value)
	if err != nil || response.ID == "" || response.Amount.Currency == "" {
		return domain.ProviderPayment{}, &domain.ProviderError{Kind: "temporary", Code: "invalid_payment"}
	}
	if response.Status != "pending" && response.Status != "succeeded" && response.Status != "canceled" {
		return domain.ProviderPayment{}, &domain.ProviderError{Kind: "temporary", Code: "unknown_status"}
	}
	var confirmationURL *string
	if response.Confirmation != nil && response.Confirmation.ConfirmationURL != "" {
		value := response.Confirmation.ConfirmationURL
		confirmationURL = &value
	}
	return domain.ProviderPayment{
		ProviderPaymentID: response.ID,
		Status:            response.Status,
		AmountMinor:       minor,
		Currency:          response.Amount.Currency,
		ConfirmationURL:   confirmationURL,
		Test:              response.Test,
		AccountID:         response.Recipient.AccountID,
		MetadataOrderID:   response.Metadata["order_id"],
		MetadataPaymentID: response.Metadata["payment_id"],
		CapturedAt:        response.CapturedAt,
		CanceledAt:        response.CanceledAt,
	}, nil
}

func formatMinor(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

func parseMinor(value string) (int64, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[1]) != 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid amount")
	}
	major, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || major < 0 {
		return 0, fmt.Errorf("invalid amount")
	}
	minor, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || minor < 0 || minor > 99 {
		return 0, fmt.Errorf("invalid amount")
	}
	if major > (1<<63-1-minor)/100 {
		return 0, fmt.Errorf("amount overflow")
	}
	return major*100 + minor, nil
}

func shortID(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
