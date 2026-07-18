package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

func TestGetSubscriptionReturnsNoStoreResponse(t *testing.T) {
	store := &handlerStore{subscription: domain.Subscription{SubscriptionID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", Status: domain.StatusActive}}
	handler := New(store)
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/users/22222222-2222-4222-8222-222222222222/subscription", nil)
	req.SetPathValue("user_id", store.subscription.UserID)
	recorder := httptest.NewRecorder()
	handler.GetSubscription(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Body.String(), `"status":"active"`) {
		t.Fatalf("status=%d cache=%q body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
}

func TestGetSubscriptionRejectsInvalidUserID(t *testing.T) {
	handler := New(&handlerStore{})
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/users/not-a-uuid/subscription", nil)
	req.SetPathValue("user_id", "not-a-uuid")
	recorder := httptest.NewRecorder()
	handler.GetSubscription(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
}

type handlerStore struct {
	domain.Store
	subscription domain.Subscription
	err          error
}

func (s *handlerStore) GetSubscription(context.Context, string) (domain.Subscription, error) {
	return s.subscription, s.err
}
