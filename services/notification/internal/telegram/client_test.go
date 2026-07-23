package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

func TestDeliverValidatesHTTPAndBodySemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantReplay bool
		wantCode   string
		wantRetry  bool
	}{
		{name: "delivered", statusCode: http.StatusOK, body: `{"status":"delivered"}`},
		{name: "replay", statusCode: http.StatusOK, body: `{"status":"replay"}`, wantReplay: true},
		{name: "rate limited", statusCode: http.StatusServiceUnavailable, body: `{"status":"retryable","reason_code":"telegram_rate_limited","retry_after_seconds":3}`, wantCode: "telegram_rate_limited", wantRetry: true},
		{name: "blocked", statusCode: http.StatusUnprocessableEntity, body: `{"status":"permanent","reason_code":"telegram_bot_blocked"}`, wantCode: "telegram_bot_blocked"},
		{name: "success on server error", statusCode: http.StatusInternalServerError, body: `{"status":"delivered"}`, wantCode: "telegram_invalid_response", wantRetry: true},
		{name: "permanent on success", statusCode: http.StatusOK, body: `{"status":"permanent","reason_code":"telegram_bot_blocked"}`, wantCode: "telegram_invalid_response", wantRetry: true},
		{name: "unknown field", statusCode: http.StatusOK, body: `{"status":"delivered","token":"forbidden"}`, wantCode: "telegram_invalid_response", wantRetry: true},
		{name: "excessive retry", statusCode: http.StatusServiceUnavailable, body: `{"status":"retryable","reason_code":"telegram_rate_limited","retry_after_seconds":901}`, wantCode: "telegram_invalid_response", wantRetry: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			result, err := client.Deliver(context.Background(), "delivery-id", 123, "access_ready", 1, "message")
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("Deliver() error = %v", err)
				}
				if result.Replay != test.wantReplay {
					t.Fatalf("Replay = %v, want %v", result.Replay, test.wantReplay)
				}
				return
			}
			var deliveryErr *domain.DeliveryError
			if !errors.As(err, &deliveryErr) {
				t.Fatalf("Deliver() error = %T %v, want DeliveryError", err, err)
			}
			if deliveryErr.Code != test.wantCode || deliveryErr.Retryable != test.wantRetry {
				t.Fatalf("DeliveryError = %#v, want code %q retryable %v", deliveryErr, test.wantCode, test.wantRetry)
			}
			if test.name == "rate limited" && deliveryErr.RetryAfter != 3*time.Second {
				t.Fatalf("RetryAfter = %v, want 3s", deliveryErr.RetryAfter)
			}
		})
	}
}
