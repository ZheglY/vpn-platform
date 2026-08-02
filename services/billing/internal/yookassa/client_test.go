package yookassa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

func TestCreatePayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "shop-1" || password != "secret-value" {
			t.Error("missing basic auth")
		}
		if got := r.Header.Get("Idempotence-Key"); got != "idem-1" {
			t.Errorf("Idempotence-Key = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		amountBody := body["amount"].(map[string]any)
		if amountBody["value"] != "299.00" || body["capture"] != true {
			t.Errorf("unexpected body: %+v", body)
		}
		if _, exists := body["receipt"]; exists {
			t.Error("receipt must not be sent in sandbox stage")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"provider-1","status":"pending","amount":{"value":"299.00","currency":"RUB"},"confirmation":{"type":"redirect","confirmation_url":"https://pay.invalid/provider-1"},"metadata":{"order_id":"order-1","payment_id":"payment-1"},"test":true,"recipient":{"account_id":"shop-1"}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "shop-1", "secret-value", "https://example.invalid/return", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	payment, err := client.CreatePayment(context.Background(), domain.ProviderCreateRequest{IdempotencyKey: "idem-1", OrderID: "order-1", PaymentID: "payment-1", AmountMinor: 29900, Currency: "RUB"})
	if err != nil {
		t.Fatal(err)
	}
	if payment.ProviderPaymentID != "provider-1" || payment.AmountMinor != 29900 || payment.ConfirmationURL == nil {
		t.Fatalf("unexpected payment: %+v", payment)
	}
}

func TestCreatePaymentTreatsServerErrorAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer server.Close()
	client, _ := NewClient(server.URL, "shop", "secret", "https://example.invalid/return", server.Client())
	_, err := client.CreatePayment(context.Background(), domain.ProviderCreateRequest{IdempotencyKey: "idem", OrderID: "order", PaymentID: "payment", AmountMinor: 100, Currency: "RUB"})
	var providerErr *domain.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != "ambiguous" {
		t.Fatalf("error = %#v", err)
	}
}

func TestTransportErrorDoesNotExposeSecret(t *testing.T) {
	httpClient := &http.Client{Timeout: time.Second, Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic transport failure") })}
	client, _ := NewClient("https://api.invalid", "shop", "top-secret-value", "https://example.invalid/return", httpClient)
	_, err := client.GetPayment(context.Background(), "provider-1")
	if err == nil || strings.Contains(err.Error(), "top-secret-value") || strings.Contains(err.Error(), "api.invalid") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestNewClientRejectsInsecureReturnURL(t *testing.T) {
	if _, err := NewClient("https://api.invalid", "shop", "secret", "http://example.invalid/return", nil); err == nil {
		t.Fatal("NewClient accepted an insecure payment return URL")
	}
}

func TestParseMinorRejectsNonCanonicalValue(t *testing.T) {
	for _, value := range []string{"1", "1.2", "1.000", "-1.00", "1e2"} {
		if _, err := parseMinor(value); err == nil {
			t.Fatalf("parseMinor(%q) succeeded", value)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
