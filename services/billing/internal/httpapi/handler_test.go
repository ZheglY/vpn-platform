package httpapi

import (
	"context"
	"strings"
	"testing"

	"net/http"
	"net/http/httptest"

	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type fakeBilling struct {
	order   domain.Order
	payment domain.Payment
}

func (f fakeBilling) CreateOrder(context.Context, string, string, string, string, string) (domain.Order, bool, error) {
	return f.order, true, nil
}
func (f fakeBilling) GetOrder(context.Context, string, string) (domain.Order, error) {
	return f.order, nil
}
func (f fakeBilling) CreatePayment(context.Context, string, string, string) (domain.Payment, bool, error) {
	return f.payment, true, nil
}

type fakeInbox struct {
	notification domain.WebhookNotification
	calls        int
}

func (f *fakeInbox) InsertWebhook(_ context.Context, n domain.WebhookNotification) (bool, error) {
	f.notification = n
	f.calls++
	return true, nil
}

func TestWebhookStoresOnlyNormalizedFields(t *testing.T) {
	inbox := &fakeInbox{}
	handler := New(fakeBilling{}, inbox)
	body := `{"type":"notification","event":"payment.succeeded","object":{"id":"provider-1","status":"succeeded","payment_method":{"card":{"last4":"4444"}},"metadata":{"secret":"must-not-be-stored"}}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/yookassa", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.YooKassaWebhook(rec, req)
	if rec.Code != http.StatusOK || inbox.calls != 1 {
		t.Fatalf("status=%d calls=%d", rec.Code, inbox.calls)
	}
	if inbox.notification.ProviderObjectID != "provider-1" || inbox.notification.ObservedStatus != "succeeded" {
		t.Fatalf("notification=%+v", inbox.notification)
	}
}

func TestWebhookRejectsEventStatusMismatch(t *testing.T) {
	handler := New(fakeBilling{}, &fakeInbox{})
	body := `{"type":"notification","event":"payment.succeeded","object":{"id":"provider-1","status":"pending"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/yookassa", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.YooKassaWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestWebhookRejectsUnknownTopLevelField(t *testing.T) {
	handler := New(fakeBilling{}, &fakeInbox{})
	body := `{"type":"notification","event":"payment.succeeded","object":{"id":"provider-1","status":"succeeded"},"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/yookassa", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.YooKassaWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
