package access

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsCurrentReadyValidatesOwnerResponse(t *testing.T) {
	t.Parallel()
	const subscriptionID = "00000000-0000-4000-8000-000000000001"
	const credentialID = "00000000-0000-4000-8000-000000000002"
	tests := []struct {
		name    string
		body    string
		want    bool
		wantErr bool
	}{
		{name: "current ready", body: `{"subscription_id":"` + subscriptionID + `","credential_id":"` + credentialID + `","access_status":"active","provisioning_status":"active","token_status":"active"}`, want: true},
		{name: "different credential", body: `{"subscription_id":"` + subscriptionID + `","credential_id":"00000000-0000-4000-8000-000000000003","access_status":"active","provisioning_status":"active","token_status":"active"}`},
		{name: "wrong subscription", body: `{"subscription_id":"00000000-0000-4000-8000-000000000004","credential_id":"` + credentialID + `","access_status":"active","provisioning_status":"active","token_status":"active"}`, wantErr: true},
		{name: "unknown field", body: `{"subscription_id":"` + subscriptionID + `","credential_id":"` + credentialID + `","access_status":"active","provisioning_status":"active","token_status":"active","vless_client_uuid":"forbidden"}`, wantErr: true},
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
			ready, err := client.IsCurrentReady(context.Background(), subscriptionID, credentialID)
			if (err != nil) != test.wantErr {
				t.Fatalf("IsCurrentReady() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && ready != test.want {
				t.Fatalf("ready = %v, want %v", ready, test.want)
			}
		})
	}
}
