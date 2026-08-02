package owner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
)

func TestReadIdentityUsesAdminOwnedDTO(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "known fields", body: `{"user_id":"00000000-0000-4000-8000-000000000001","status":"active","locale":"en"}`},
		{name: "unknown field", body: `{"user_id":"00000000-0000-4000-8000-000000000001","status":"active","subscription_url":"forbidden"}`, wantErr: true},
		{name: "trailing JSON", body: `{"user_id":"00000000-0000-4000-8000-000000000001","status":"active"}{}`, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(Config{
				IdentityBaseURL: server.URL, BillingBaseURL: server.URL, SubscriptionBaseURL: server.URL,
				AccessBaseURL: server.URL, ProvisioningBaseURL: server.URL, NotificationBaseURL: server.URL,
			}, server.Client())
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			payload, err := client.ReadIdentity(context.Background(), "00000000-0000-4000-8000-000000000001")
			if !test.wantErr {
				if err != nil {
					t.Fatalf("ReadIdentity() error = %v", err)
				}
				if string(payload) != `{"user_id":"00000000-0000-4000-8000-000000000001","status":"active","locale":"en"}` {
					t.Fatalf("payload = %s", payload)
				}
				return
			}
			var ownerErr *domain.OwnerError
			if !errors.As(err, &ownerErr) || ownerErr.Code != "owner_response_invalid" {
				t.Fatalf("ReadIdentity() error = %T %v, want owner_response_invalid", err, err)
			}
		})
	}
}

func TestRecoverAccessRejectsSemanticOwnerMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"credential_id":"00000000-0000-4000-8000-000000000099","operation_id":"00000000-0000-4000-8000-000000000003","desired_revision":2,"status":"provisioning","replay":false}`))
	}))
	defer server.Close()
	client, err := New(Config{
		IdentityBaseURL: server.URL, BillingBaseURL: server.URL, SubscriptionBaseURL: server.URL,
		AccessBaseURL: server.URL, ProvisioningBaseURL: server.URL, NotificationBaseURL: server.URL,
	}, server.Client())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.RecoverAccess(context.Background(), "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000010", "00000000-0000-4000-8000-000000000011")
	var ownerErr *domain.OwnerError
	if !errors.As(err, &ownerErr) || ownerErr.Code != "owner_response_invalid" || ownerErr.Definitive {
		t.Fatalf("RecoverAccess() error = %T %v, want owner_response_invalid", err, err)
	}
}

func TestMutationOwnerOutcomeClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		status         int
		body           string
		wantCode       string
		wantDefinitive bool
	}{
		{name: "definitive conflict", status: http.StatusConflict, body: `{}`, wantCode: "owner_conflict", wantDefinitive: true},
		{name: "unknown server failure", status: http.StatusServiceUnavailable, body: `{}`, wantCode: "owner_unavailable"},
		{name: "unknown invalid success", status: http.StatusOK, body: `{"subscription_id":`, wantCode: "owner_response_invalid"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(Config{
				IdentityBaseURL: server.URL, BillingBaseURL: server.URL, SubscriptionBaseURL: server.URL,
				AccessBaseURL: server.URL, ProvisioningBaseURL: server.URL, NotificationBaseURL: server.URL,
			}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.RevokeSubscription(context.Background(), "00000000-0000-4000-8000-000000000002",
				"00000000-0000-4000-8000-000000000010", "00000000-0000-4000-8000-000000000011", "admin_block")
			var ownerErr *domain.OwnerError
			if !errors.As(err, &ownerErr) || ownerErr.Code != test.wantCode || ownerErr.Definitive != test.wantDefinitive {
				t.Fatalf("error=%T %v definitive=%v", err, err, ownerErr != nil && ownerErr.Definitive)
			}
		})
	}
}
