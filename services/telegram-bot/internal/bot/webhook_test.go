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
	"sync/atomic"
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

func TestWebhookRateLimit(t *testing.T) {
	handler := NewWebhookHandler(Config{
		WebhookSecret:  "secret",
		ConsentVersion: "terms-v1",
		TermsURL:       "https://example.invalid/terms/terms-v1",
	}, &fakeIdentity{}, &fakeTelegram{}, newMemoryStore(), newMemoryStore(), &fakeRateLimiter{allowed: false}, zap.NewNop())

	rec := postUpdate(handler, startUpdate(10, 42, "/start"))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	identity := handler.identity.(*fakeIdentity)
	if identity.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", identity.upserts)
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
	messages := telegram.messagesSnapshot()
	if len(messages) != 1 || !strings.Contains(messages[0], "terms-v1") {
		t.Fatalf("messages = %#v", messages)
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
	if count := telegram.messageCount(); count != 1 {
		t.Fatalf("messages = %d, want 1", count)
	}
}

func TestWebhookConcurrentDuplicateWhileProcessingReturnsRetryableStatus(t *testing.T) {
	handler := newTestHandler(t)
	telegram := handler.telegram.(*fakeTelegram)
	telegram.started = make(chan struct{})
	telegram.release = make(chan struct{})

	var firstStatus atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		firstStatus.Store(int32(postUpdate(handler, startUpdate(14, 42, "/start")).Code))
	}()

	<-telegram.started
	second := postUpdate(handler, startUpdate(14, 42, "/start"))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusServiceUnavailable)
	}

	close(telegram.release)
	<-done
	if firstStatus.Load() != http.StatusOK {
		t.Fatalf("first status = %d, want %d", firstStatus.Load(), http.StatusOK)
	}
	identity := handler.identity.(*fakeIdentity)
	if identity.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", identity.upserts)
	}
	if count := telegram.messageCount(); count != 1 {
		t.Fatalf("messages = %d, want 1", count)
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
	if count := telegram.messageCount(); count != 1 {
		t.Fatalf("messages = %d, want 1", count)
	}
}

func TestWebhookFailureCleanupIgnoresCanceledRequestContext(t *testing.T) {
	handler := newTestHandler(t)
	telegram := handler.telegram.(*fakeTelegram)
	telegram.err = errors.New("temporary Telegram API failure")

	req := newUpdateRequest(startUpdate(15, 42, "/start"))
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusServiceUnavailable)
	}
	store := handler.dedupe.(*memoryStore)
	if store.releaseSawCanceled {
		t.Fatal("release used canceled request context")
	}

	telegram.err = nil
	second := postUpdate(handler, startUpdate(15, 42, "/start"))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusOK)
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
	messages := telegram.messagesSnapshot()
	if len(messages) != 1 || !strings.Contains(messages[0], "Menu") {
		t.Fatalf("messages = %#v", messages)
	}
	state, _ := handler.fsm.GetState(context.Background(), 42)
	if state != StateMenu {
		t.Fatalf("state = %q, want %q", state, StateMenu)
	}
}

func TestWebhookBuyCreatesOneOrderAndPayment(t *testing.T) {
	identity := &fakeIdentity{consented: true}
	telegram := &fakeTelegram{}
	catalog := &fakeCatalog{plans: []Plan{{PlanID: "vpn-30d-v1", Name: "VPN 30 days", DurationDays: 30, AmountMinor: 29900, Currency: "RUB", Regions: []string{"ru-test"}}}}
	billing := &fakeBilling{order: Order{OrderID: "00000000-0000-4000-8000-000000000002"}, payment: Payment{Status: "pending", ConfirmationURL: stringPointer("https://pay.invalid/payment")}}
	handler := NewWebhookHandlerWithCommerce(Config{WebhookSecret: "secret", ConsentVersion: "terms-v1", TermsURL: "https://example.invalid/terms/terms-v1"}, identity, catalog, billing, telegram, newMemoryStore(), newMemoryStore(), nil, zap.NewNop())

	rec := postUpdate(handler, startUpdate(17, 42, "/buy"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if billing.orderCalls != 1 || billing.paymentCalls != 1 {
		t.Fatalf("billing calls = order %d payment %d, want 1 each", billing.orderCalls, billing.paymentCalls)
	}
	if billing.orderKey != "tg:17:order" || billing.paymentKey != "tg:17:payment" {
		t.Fatalf("idempotency keys = %q %q", billing.orderKey, billing.paymentKey)
	}
	messages := telegram.messagesSnapshot()
	if len(messages) != 1 || !strings.Contains(messages[0], "https://pay.invalid/payment") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestWebhookBuyWithoutConsentPromptsInsteadOfRetrying(t *testing.T) {
	handler := NewWebhookHandlerWithCommerce(Config{WebhookSecret: "secret", ConsentVersion: "terms-v1", TermsURL: "https://example.invalid/terms/terms-v1"}, &fakeIdentity{}, &fakeCatalog{}, &fakeBilling{}, &fakeTelegram{}, newMemoryStore(), newMemoryStore(), nil, zap.NewNop())

	rec := postUpdate(handler, startUpdate(18, 42, "/buy"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	state, _ := handler.fsm.GetState(context.Background(), 42)
	if state != StateAwaitingConsent {
		t.Fatalf("state = %q, want %q", state, StateAwaitingConsent)
	}
	if messages := handler.telegram.(*fakeTelegram).messagesSnapshot(); len(messages) != 1 || !strings.Contains(messages[0], "terms-v1") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestWebhookBlockedUserDoesNotEnterConsentFlow(t *testing.T) {
	handler := newTestHandler(t)
	identity := handler.identity.(*fakeIdentity)
	identity.status = "blocked"

	rec := postUpdate(handler, startUpdate(16, 42, "/start"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if identity.consentChecks != 0 {
		t.Fatalf("consent checks = %d, want 0", identity.consentChecks)
	}
	state, _ := handler.fsm.GetState(context.Background(), 42)
	if state != "" {
		t.Fatalf("state = %q, want empty", state)
	}
	telegram := handler.telegram.(*fakeTelegram)
	messages := telegram.messagesSnapshot()
	if len(messages) != 1 || !strings.Contains(messages[0], "unavailable") {
		t.Fatalf("messages = %#v", messages)
	}
}

func newTestHandler(t *testing.T) *WebhookHandler {
	t.Helper()
	return NewWebhookHandler(Config{
		WebhookSecret:  "secret",
		ConsentVersion: "terms-v1",
		TermsURL:       "https://example.invalid/terms/terms-v1",
	}, &fakeIdentity{}, &fakeTelegram{}, newMemoryStore(), newMemoryStore(), nil, zap.NewNop())
}

func postUpdate(handler http.Handler, body string) *httptest.ResponseRecorder {
	req := newUpdateRequest(body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newUpdateRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", bytes.NewBufferString(body))
	req.Header.Set(telegramSecretHeader, "secret")
	return req
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
	upserts       int
	accepts       int
	consentChecks int
	status        string
	consented     bool
}

func (f *fakeIdentity) UpsertTelegramIdentity(context.Context, TelegramProfile) (IdentityUser, error) {
	f.upserts++
	status := f.status
	if status == "" {
		status = "active"
	}
	return IdentityUser{UserID: "00000000-0000-4000-8000-000000000001", Status: status}, nil
}

func (f *fakeIdentity) HasConsent(context.Context, string, string, string) (bool, error) {
	f.consentChecks++
	return f.consented, nil
}

func (f *fakeIdentity) AcceptConsent(context.Context, string, string, string) error {
	f.accepts++
	return nil
}

type fakeTelegram struct {
	mu          sync.Mutex
	messages    []string
	err         error
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
}

type fakeCatalog struct {
	plans []Plan
	err   error
}

func (f *fakeCatalog) ListPlans(context.Context) ([]Plan, error) {
	return append([]Plan(nil), f.plans...), f.err
}

type fakeBilling struct {
	order        Order
	payment      Payment
	err          error
	orderCalls   int
	paymentCalls int
	orderKey     string
	paymentKey   string
}

func (f *fakeBilling) CreateOrder(_ context.Context, _, _, _, _, idempotencyKey string) (Order, error) {
	f.orderCalls++
	f.orderKey = idempotencyKey
	return f.order, f.err
}

func (f *fakeBilling) CreatePayment(_ context.Context, _, _, idempotencyKey string) (Payment, error) {
	f.paymentCalls++
	f.paymentKey = idempotencyKey
	return f.payment, f.err
}

func stringPointer(value string) *string { return &value }

func (f *fakeTelegram) SendMessage(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	err := f.err
	started := f.started
	release := f.release
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if started != nil {
		f.startedOnce.Do(func() {
			close(started)
		})
	}
	if release != nil {
		<-release
	}
	f.mu.Lock()
	f.messages = append(f.messages, text)
	f.mu.Unlock()
	return nil
}

func (f *fakeTelegram) messageCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *fakeTelegram) messagesSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

type memoryStore struct {
	mu                 sync.Mutex
	processing         map[int64]string
	completed          map[int64]struct{}
	states             map[int64]string
	releaseSawCanceled bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		processing: make(map[int64]string),
		completed:  make(map[int64]struct{}),
		states:     make(map[int64]string),
	}
}

func (s *memoryStore) StartProcessing(_ context.Context, updateID int64, token string) (DedupeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.completed[updateID]; ok {
		return DedupeCompleted, nil
	}
	if _, ok := s.processing[updateID]; ok {
		return DedupeProcessing, nil
	}
	s.processing[updateID] = token
	return DedupeAcquired, nil
}

func (s *memoryStore) CompleteProcessing(_ context.Context, updateID int64, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed[updateID] = struct{}{}
	if s.processing[updateID] == token {
		delete(s.processing, updateID)
	}
	return nil
}

func (s *memoryStore) ReleaseProcessing(ctx context.Context, updateID int64, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		s.releaseSawCanceled = true
	}
	if s.processing[updateID] == token {
		delete(s.processing, updateID)
	}
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

type fakeRateLimiter struct {
	allowed bool
	err     error
}

func (l *fakeRateLimiter) Allow(context.Context, string) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	return l.allowed, nil
}
