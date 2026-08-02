package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNotificationTargetValidatesOwnerResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		wantErr  bool
		eligible bool
	}{
		{name: "eligible", body: `{"eligible":true,"telegram_chat_id":123,"locale":"en"}`, eligible: true},
		{name: "ineligible", body: `{"eligible":false,"reason_code":"consent_missing","locale":"en"}`},
		{name: "chat on ineligible", body: `{"eligible":false,"reason_code":"consent_missing","telegram_chat_id":123}`, wantErr: true},
		{name: "reason on eligible", body: `{"eligible":true,"reason_code":"unexpected","telegram_chat_id":123}`, wantErr: true},
		{name: "missing reason", body: `{"eligible":false}`, wantErr: true},
		{name: "unknown field", body: `{"eligible":true,"telegram_chat_id":123,"subscription_url":"forbidden"}`, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "terms", "v1", server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			result, err := client.GetNotificationTarget(context.Background(), "00000000-0000-4000-8000-000000000001")
			if (err != nil) != test.wantErr {
				t.Fatalf("GetNotificationTarget() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && result.Eligible != test.eligible {
				t.Fatalf("Eligible = %v, want %v", result.Eligible, test.eligible)
			}
		})
	}
}
