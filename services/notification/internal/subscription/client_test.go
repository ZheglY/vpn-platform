package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetStateValidatesOwnerResponse(t *testing.T) {
	t.Parallel()
	const userID = "00000000-0000-4000-8000-000000000001"
	const subscriptionID = "00000000-0000-4000-8000-000000000002"
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "active", body: `{"subscription_id":"` + subscriptionID + `","user_id":"` + userID + `","status":"active","current_period_start":null,"current_period_end":null,"grace_ends_at":null}`, want: "active"},
		{name: "grace", body: `{"subscription_id":"` + subscriptionID + `","user_id":"` + userID + `","status":"grace","current_period_start":null,"current_period_end":null,"grace_ends_at":null}`, want: "grace"},
		{name: "revoked", body: `{"subscription_id":"` + subscriptionID + `","user_id":"` + userID + `","status":"revoked","current_period_start":null,"current_period_end":null,"grace_ends_at":null}`, want: "revoked"},
		{name: "wrong aggregate", body: `{"subscription_id":"00000000-0000-4000-8000-000000000003","user_id":"` + userID + `","status":"active","current_period_start":null,"current_period_end":null,"grace_ends_at":null}`, wantErr: true},
		{name: "unknown status", body: `{"subscription_id":"` + subscriptionID + `","user_id":"` + userID + `","status":"unknown","current_period_start":null,"current_period_end":null,"grace_ends_at":null}`, wantErr: true},
		{name: "secret field", body: `{"subscription_id":"` + subscriptionID + `","user_id":"` + userID + `","status":"active","current_period_start":null,"current_period_end":null,"grace_ends_at":null,"subscription_url":"forbidden"}`, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			state, err := client.GetState(context.Background(), userID, subscriptionID)
			if (err != nil) != test.wantErr {
				t.Fatalf("GetState() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && state.Status != test.want {
				t.Fatalf("status = %q, want %q", state.Status, test.want)
			}
		})
	}
}
