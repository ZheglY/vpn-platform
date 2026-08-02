package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	telegramapi "github.com/ZheglY/vpn-platform/services/telegram-bot/internal/telegram"
)

func TestDeliveryReplayDoesNotCallTelegram(t *testing.T) {
	store := &deliveryStore{state: "completed"}
	telegram := &deliveryTelegram{}
	handler := NewDeliveryHandler(store, telegram)
	recorder := invokeDelivery(handler, validDeliveryBody())
	if recorder.Code != http.StatusOK || telegram.calls != 0 || !strings.Contains(recorder.Body.String(), `"status":"replay"`) {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, telegram.calls, recorder.Body.String())
	}
}

func TestDecodeDeliveryJSONRejectsOversizedBody(t *testing.T) {
	var request struct {
		Text string `json:"text"`
	}
	if err := decodeDeliveryJSON(strings.NewReader(strings.Repeat(" ", maxDeliveryBodyBytes+1)), &request); err == nil {
		t.Fatal("oversized delivery body accepted")
	}
}

func TestDeliveryClassifiesTelegramErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		result string
		reason string
	}{
		{"rate-limit", &telegramapi.APIError{Kind: telegramapi.ErrorKindRateLimited, RetryAfter: 3 * time.Second}, http.StatusServiceUnavailable, "retryable", "telegram_rate_limited"},
		{"blocked", &telegramapi.APIError{Kind: telegramapi.ErrorKindAPI, ErrorCode: 403}, http.StatusUnprocessableEntity, "permanent", "telegram_bot_blocked"},
		{"network", &telegramapi.APIError{Kind: telegramapi.ErrorKindNetwork}, http.StatusServiceUnavailable, "retryable", "telegram_temporary"},
		{"unknown", errors.New("unknown"), http.StatusServiceUnavailable, "retryable", "telegram_unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, result, reason, _ := classifyTelegramDelivery(test.err)
			if status != test.status || result != test.result || reason != test.reason {
				t.Fatalf("status=%d result=%q reason=%q", status, result, reason)
			}
		})
	}
}

func TestDeliveryRejectsSecretBearingText(t *testing.T) {
	handler := NewDeliveryHandler(&deliveryStore{state: "acquired"}, &deliveryTelegram{})
	recorder := invokeDelivery(handler, `{"telegram_chat_id":1234,"notification_type":"access_ready","template_version":1,"text":"vless://secret"}`)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"status":"permanent"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type deliveryStore struct {
	state     string
	released  int
	completed int
}

func (s *deliveryStore) StartDelivery(context.Context, string, string, string) (string, error) {
	return s.state, nil
}
func (s *deliveryStore) CompleteDelivery(context.Context, string, string, string) error {
	s.completed++
	return nil
}
func (s *deliveryStore) ReleaseDelivery(context.Context, string, string, string) error {
	s.released++
	return nil
}

type deliveryTelegram struct {
	calls int
	err   error
}

func (t *deliveryTelegram) SendHTMLMessage(context.Context, int64, string) error {
	t.calls++
	return t.err
}

func invokeDelivery(handler *DeliveryHandler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/notifications/11111111-1111-4111-8111-111111111111/telegram", strings.NewReader(body))
	request.SetPathValue("delivery_id", "11111111-1111-4111-8111-111111111111")
	recorder := httptest.NewRecorder()
	handler.Deliver(recorder, request)
	return recorder
}

func validDeliveryBody() string {
	return `{"telegram_chat_id":1234,"notification_type":"access_ready","template_version":1,"text":"<b>VPN ready.</b>"}`
}
