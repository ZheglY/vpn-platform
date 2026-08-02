package application

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

func TestWorkerHonorsRetryAfter(t *testing.T) {
	store := &workerStore{job: testJob()}
	worker := NewWorker(store, eligibleIdentity{}, subscriptionState("active"), readyAccess(true), failingTelegram{err: &domain.DeliveryError{Code: "telegram_rate_limited", Retryable: true, RetryAfter: 17 * time.Second}}, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.retryDelay != 17*time.Second || store.failedCode != "" {
		t.Fatalf("retry=%s failed=%q", store.retryDelay, store.failedCode)
	}
}

func TestWorkerSuppressesStaleAccessReady(t *testing.T) {
	store := &workerStore{job: testJob()}
	telegram := &countingTelegram{}
	worker := NewWorker(store, eligibleIdentity{}, subscriptionState("active"), readyAccess(false), telegram, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.suppressedCode != "stale_access_state" || telegram.calls != 0 {
		t.Fatalf("suppressed=%q telegram_calls=%d", store.suppressedCode, telegram.calls)
	}
}

func TestWorkerSuppressesAccessAfterSubscriptionTerminalState(t *testing.T) {
	store := &workerStore{job: testJob()}
	telegram := &countingTelegram{}
	worker := NewWorker(store, eligibleIdentity{}, subscriptionState("revoked"), readyAccess(true), telegram, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.suppressedCode != "stale_subscription_state" || telegram.calls != 0 {
		t.Fatalf("suppressed=%q telegram_calls=%d", store.suppressedCode, telegram.calls)
	}
}

func TestWorkerSuppressesExtensionAfterSubscriptionRevoked(t *testing.T) {
	job := testJob()
	job.NotificationType = "subscription_extended"
	job.CredentialID = nil
	job.Variables = map[string]string{"period_end": "2026-08-18T12:00:00Z"}
	store := &workerStore{job: job}
	telegram := &countingTelegram{}
	worker := NewWorker(store, eligibleIdentity{}, subscriptionState("revoked"), readyAccess(true), telegram, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.suppressedCode != "stale_subscription_state" || telegram.calls != 0 {
		t.Fatalf("suppressed=%q telegram_calls=%d", store.suppressedCode, telegram.calls)
	}
}

func TestWorkerSuppressesGraceAfterSubscriptionRenewal(t *testing.T) {
	job := testJob()
	job.NotificationType = "subscription_grace"
	job.CredentialID = nil
	job.Variables = map[string]string{"grace_ends_at": "2026-08-19T12:00:00Z"}
	store := &workerStore{job: job}
	telegram := &countingTelegram{}
	periodEnd := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	state := fixedSubscriptionState{state: domain.SubscriptionState{Status: "active", CurrentPeriodEnd: &periodEnd}}
	worker := NewWorker(store, eligibleIdentity{}, state, readyAccess(true), telegram, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.suppressedCode != "stale_subscription_state" || telegram.calls != 0 {
		t.Fatalf("suppressed=%q telegram_calls=%d", store.suppressedCode, telegram.calls)
	}
}

func TestWorkerDeliversCurrentExtension(t *testing.T) {
	job := testJob()
	job.NotificationType = "subscription_extended"
	job.CredentialID = nil
	job.Variables = map[string]string{"period_end": "2026-08-18T12:00:00Z"}
	store := &workerStore{job: job}
	telegram := &countingTelegram{}
	periodEnd := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	state := fixedSubscriptionState{state: domain.SubscriptionState{Status: "active", CurrentPeriodEnd: &periodEnd}}
	worker := NewWorker(store, eligibleIdentity{}, state, readyAccess(true), telegram, zap.NewNop(), time.Millisecond, time.Second, time.Minute)
	if err := worker.workOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.suppressedCode != "" || telegram.calls != 1 {
		t.Fatalf("suppressed=%q telegram_calls=%d", store.suppressedCode, telegram.calls)
	}
}

type workerStore struct {
	domain.Store
	job            domain.Job
	retryDelay     time.Duration
	failedCode     string
	suppressedCode string
}

func (s *workerStore) ClaimJob(context.Context, time.Duration) (domain.Job, bool, error) {
	return s.job, true, nil
}
func (s *workerStore) RetryJob(_ context.Context, _, _ string, delay time.Duration) error {
	s.retryDelay = delay
	return nil
}
func (s *workerStore) FailJob(_ context.Context, _, _, code string) error {
	s.failedCode = code
	return nil
}
func (s *workerStore) SuppressJob(_ context.Context, _, _, code string) error {
	s.suppressedCode = code
	return nil
}
func (s *workerStore) CompleteJob(context.Context, string, string) error { return nil }

type eligibleIdentity struct{ domain.IdentityClient }

func (eligibleIdentity) GetNotificationTarget(context.Context, string) (domain.TelegramTarget, error) {
	return domain.TelegramTarget{Eligible: true, TelegramChatID: 1234}, nil
}

type readyAccess bool

func (r readyAccess) IsCurrentReady(context.Context, string, string) (bool, error) {
	return bool(r), nil
}

type subscriptionState string

func (s subscriptionState) GetState(context.Context, string, string) (domain.SubscriptionState, error) {
	return domain.SubscriptionState{Status: string(s)}, nil
}

type fixedSubscriptionState struct {
	state domain.SubscriptionState
}

func (s fixedSubscriptionState) GetState(context.Context, string, string) (domain.SubscriptionState, error) {
	return s.state, nil
}

type failingTelegram struct {
	domain.TelegramClient
	err error
}

func (f failingTelegram) Deliver(context.Context, string, int64, string, int, string) (domain.DeliveryResult, error) {
	return domain.DeliveryResult{}, f.err
}

type countingTelegram struct {
	domain.TelegramClient
	calls int
}

func (t *countingTelegram) Deliver(context.Context, string, int64, string, int, string) (domain.DeliveryResult, error) {
	t.calls++
	return domain.DeliveryResult{}, nil
}

func testJob() domain.Job {
	subscriptionID := "11111111-1111-4111-8111-111111111111"
	credentialID := "22222222-2222-4222-8222-222222222222"
	return domain.Job{
		NotificationID: "33333333-3333-4333-8333-333333333333", UserID: "44444444-4444-4444-8444-444444444444",
		SubscriptionID: &subscriptionID, CredentialID: &credentialID, NotificationType: "access_ready", TemplateVersion: 1,
		Attempts: 1, MaxAttempts: 3, ClaimID: "55555555-5555-4555-8555-555555555555", Variables: map[string]string{},
	}
}
