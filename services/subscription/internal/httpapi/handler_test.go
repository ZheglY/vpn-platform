package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestDecodeStrictRejectsOversizedAdminRevokeBody(t *testing.T) {
	var request struct {
		ActionID string `json:"action_id"`
	}
	if err := decodeStrict(strings.NewReader(strings.Repeat(" ", maxAdminRevokeBodyBytes+1)), &request); err == nil {
		t.Fatal("oversized admin revoke body accepted")
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

func TestGetPlacementReturnsNoStoreSnapshot(t *testing.T) {
	store := &handlerStore{placement: domain.Placement{
		SubscriptionID: "11111111-1111-4111-8111-111111111111",
		PeriodID:       "33333333-3333-4333-8333-333333333333",
		Region:         "ru-test",
		PrimaryNodes:   1,
		FailoverNodes:  1,
		ValidUntil:     time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}}
	handler := New(store)
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/subscriptions/11111111-1111-4111-8111-111111111111/placement", nil)
	req.SetPathValue("subscription_id", store.placement.SubscriptionID)
	recorder := httptest.NewRecorder()
	handler.GetPlacement(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Body.String(), `"region":"ru-test"`) {
		t.Fatalf("status=%d cache=%q body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
}

type handlerStore struct {
	domain.Store
	subscription domain.Subscription
	placement    domain.Placement
	err          error
}

func (s *handlerStore) GetSubscription(context.Context, string) (domain.Subscription, error) {
	return s.subscription, s.err
}

func (s *handlerStore) GetPlacement(context.Context, string) (domain.Placement, error) {
	return s.placement, s.err
}
