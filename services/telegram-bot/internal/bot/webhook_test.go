package bot

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestWebhookRejectsInvalidSecret(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", strings.NewReader(`{"update_id":1}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookStartPromptsForConsent(t *testing.T) {
	handler := newTestHandler(t)

	rec := postUpdate(handler, startUpdate(11, 42, "/start"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	identity := handler.identity.(*fakeIdentity)
	if identity.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", identity.upserts)
	}
	telegram := handler.telegram.(*fakeTelegram)
	if len(telegram.messages) != 1 || !strings.Contains(telegram.messages[0], "terms-v1") {
		t.Fatalf("messages = %#v", telegram.messages)
	}
	state, _ := handler.fsm.GetState(context.Background(), 42)
	if state != StateAwaitingConsent {
		t.Fatalf("state = %q, want %q", state, StateAwaitingConsent)
	}
}

func TestWebhookDuplicateUpdateDoesNotRepeatSideEffects(t *testing.T) {
	handler := newTestHandler(t)

	first := postUpdate(handler, startUpdate(11, 42, "/start"))
	second := postUpdate(handler, startUpdate(11, 42, "/start"))

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	identity := handler.identity.(*fakeIdentity)
	if identity.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", identity.upserts)
	}
	telegram := handler.telegram.(*fakeTelegram)
	if len(telegram.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(telegram.messages))
	}
}

func TestWebhookProcessingFailureAllowsTelegramRetry(t *testing.T) {
	handler := newTestHandler(t)
	telegram := handler.telegram.(*fakeTelegram)
	telegram.err = errors.New("temporary Telegram API failure")

	first := postUpdate(handler, startUpdate(13, 42, "/start"))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusServiceUnavailable)
	}

	telegram.err = nil
	second := postUpdate(handler, startUpdate(13, 42, "/start"))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusOK)
	}
	if len(telegram.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(telegram.messages))
	}
}

func TestWebhookAcceptConsentShowsMenu(t *testing.T) {
	handler := newTestHandler(t)
	if err := handler.fsm.SetState(context.Background(), 42, StateAwaitingConsent); err != nil {
		t.Fatalf("set state: %v", err)
	}

	rec := postUpdate(handler, startUpdate(12, 42, "accept"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	identity := handler.identity.(*fakeIdentity)
	if identity.accepts != 1 {
		t.Fatalf("accepts = %d, want 1", identity.accepts)
	}
	telegram := handler.telegram.(*fakeTelegram)
	if len(telegram.messages) != 1 || !strings.Contains(telegram.messages[0], "Menu") {
		t.Fatalf("messages = %#v", telegram.messages)
	}
	state, _ := handler.fsm.GetState(context.Background(), 42)
	if state != StateMenu {
		t.Fatalf("state = %q, want %q", state, StateMenu)
	}
}

func newTestHandler(t *testing.T) *WebhookHandler {
	t.Helper()
	return NewWebhookHandler(Config{
		WebhookSecret:  "secret",
		ConsentVersion: "terms-v1",
	}, &fakeIdentity{}, &fakeTelegram{}, newMemoryStore(), newMemoryStore(), zap.NewNop())
}

func postUpdate(handler http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", bytes.NewBufferString(body))
	req.Header.Set(telegramSecretHeader, "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func startUpdate(updateID, telegramUserID int64, text string) string {
	return `{
  "update_id": ` + intString(updateID) + `,
  "message": {
    "message_id": 1,
    "text": "` + text + `",
    "chat": {"id": 100},
    "from": {
      "id": ` + intString(telegramUserID) + `,
      "first_name": "Test",
      "language_code": "en"
    }
  }
}`
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}

type fakeIdentity struct {
	upserts int
	accepts int
}

func (f *fakeIdentity) UpsertTelegramIdentity(context.Context, TelegramProfile) (IdentityUser, error) {
	f.upserts++
	return IdentityUser{UserID: "00000000-0000-4000-8000-000000000001", Status: "active"}, nil
}

func (f *fakeIdentity) HasConsent(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (f *fakeIdentity) AcceptConsent(context.Context, string, string, string) error {
	f.accepts++
	return nil
}

type fakeTelegram struct {
	messages []string
	err      error
}

func (f *fakeTelegram) SendMessage(_ context.Context, _ int64, text string) error {
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, text)
	return nil
}

type memoryStore struct {
	mu     sync.Mutex
	seen   map[int64]struct{}
	states map[int64]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		seen:   make(map[int64]struct{}),
		states: make(map[int64]string),
	}
}

func (s *memoryStore) MarkProcessed(_ context.Context, updateID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[updateID]; ok {
		return false, nil
	}
	s.seen[updateID] = struct{}{}
	return true, nil
}

func (s *memoryStore) ForgetProcessed(_ context.Context, updateID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, updateID)
	return nil
}

func (s *memoryStore) GetState(_ context.Context, telegramUserID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[telegramUserID], nil
}

func (s *memoryStore) SetState(_ context.Context, telegramUserID int64, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[telegramUserID] = state
	return nil
}
