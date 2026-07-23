package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/notification/internal/domain"
)

func TestIntegrationNotificationInboxDedupeOrderingAndClaims(t *testing.T) {
	store := notificationIntegrationStore(t)
	ctx := context.Background()
	intent := integrationIntent("30000000-0000-4000-8000-000000000001", "fact:one")
	meta := integrationNotificationMeta("10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000001", 1, 1)
	created, err := store.RecordEvent(ctx, meta, intent)
	if err != nil || !created {
		t.Fatalf("record first event: created=%v err=%v", created, err)
	}
	created, err = store.RecordEvent(ctx, meta, intent)
	if err != nil || created {
		t.Fatalf("exact replay: created=%v err=%v", created, err)
	}
	republished := meta
	republished.SourceOffset = 99
	created, err = store.RecordEvent(ctx, republished, intent)
	if err != nil || created {
		t.Fatalf("republished exact replay: created=%v err=%v", created, err)
	}
	collision := meta
	collision.PayloadSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.RecordEvent(ctx, collision, intent); !errors.Is(err, domain.ErrEventConflict) {
		t.Fatalf("event ID collision = %v", err)
	}

	gapMeta := integrationNotificationMeta("10000000-0000-4000-8000-000000000003", meta.AggregateID, 2, 3)
	if _, err := store.RecordEvent(ctx, gapMeta, integrationIntent("30000000-0000-4000-8000-000000000003", "fact:three")); !errors.Is(err, domain.ErrSequenceGap) {
		t.Fatalf("sequence gap = %v", err)
	}
	secondMeta := integrationNotificationMeta("10000000-0000-4000-8000-000000000002", meta.AggregateID, 3, 2)
	if _, err := store.RecordEvent(ctx, secondMeta, integrationIntent("30000000-0000-4000-8000-000000000002", "fact:two")); err != nil {
		t.Fatal(err)
	}

	businessDuplicateMeta := integrationNotificationMeta("10000000-0000-4000-8000-000000000004", "20000000-0000-4000-8000-000000000004", 4, 1)
	created, err = store.RecordEvent(ctx, businessDuplicateMeta, integrationIntent("30000000-0000-4000-8000-000000000004", "fact:one"))
	if err != nil || created {
		t.Fatalf("business duplicate: created=%v err=%v", created, err)
	}

	var jobs int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM notification_jobs`).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatalf("jobs=%d err=%v", jobs, err)
	}

	claimed := make(chan domain.Job, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			job, ok, err := store.ClaimJob(ctx, 20*time.Millisecond)
			if err != nil || !ok {
				errs <- err
				return
			}
			claimed <- job
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	close(claimed)
	seen := map[string]struct{}{}
	var first domain.Job
	for job := range claimed {
		if first.NotificationID == "" {
			first = job
		}
		seen[job.NotificationID] = struct{}{}
	}
	if len(seen) != 2 {
		t.Fatalf("concurrent claims returned %d distinct jobs", len(seen))
	}
	time.Sleep(30 * time.Millisecond)
	reclaimed, ok, err := store.ClaimJob(ctx, time.Second)
	if err != nil || !ok || reclaimed.Attempts != 2 {
		t.Fatalf("expired lease reclaim=%+v ok=%v err=%v", reclaimed, ok, err)
	}
}

func TestIntegrationNotificationDLQAndAdminRetryCollisions(t *testing.T) {
	store := notificationIntegrationStore(t)
	ctx := context.Background()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.RecordDeadLetter(ctx, "topic.v1", 0, 1, hash, "", "invalid_event_data"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDeadLetter(ctx, "topic.v1", 0, 1, hash, "", "invalid_event_data"); err != nil {
		t.Fatalf("exact DLQ replay: %v", err)
	}
	if err := store.RecordDeadLetter(ctx, "topic.v1", 0, 1, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "", "invalid_event_data"); !errors.Is(err, domain.ErrEventConflict) {
		t.Fatalf("DLQ collision = %v", err)
	}

	meta := integrationNotificationMeta("10000000-0000-4000-8000-000000000011", "20000000-0000-4000-8000-000000000011", 11, 1)
	intent := integrationIntent("30000000-0000-4000-8000-000000000011", "fact:retry")
	if _, err := store.RecordEvent(ctx, meta, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE notification_jobs SET status='permanently_failed', terminal_reason_code='telegram_blocked' WHERE notification_id=$1`, intent.NotificationID); err != nil {
		t.Fatal(err)
	}
	requestHash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, replay, err := store.RequestRetry(ctx, intent.NotificationID, "retry-key-0001", requestHash); err != nil || replay {
		t.Fatalf("retry request: replay=%v err=%v", replay, err)
	}
	if _, replay, err := store.RequestRetry(ctx, intent.NotificationID, "retry-key-0001", requestHash); err != nil || !replay {
		t.Fatalf("retry replay: replay=%v err=%v", replay, err)
	}
	if _, _, err := store.RequestRetry(ctx, intent.NotificationID, "retry-key-0001", hash); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("retry collision = %v", err)
	}
}

func notificationIntegrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("NOTIFICATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOTIFICATION_TEST_DATABASE_URL is not set")
	}
	store, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	truncate := func() error {
		_, err := store.pool.Exec(context.Background(), `TRUNCATE notification_admin_requests, notification_dead_letters, notification_jobs, notification_inbox, notification_cursors CASCADE`)
		return err
	}
	if err := truncate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := truncate(); err != nil {
			t.Errorf("clean notification integration state: %v", err)
		}
		store.Close()
	})
	return store
}

func integrationNotificationMeta(eventID, aggregateID string, offset, sequence int64) domain.EventMeta {
	return domain.EventMeta{
		EventID: eventID, EventType: "subscription.extended.v1", Producer: "subscription-service",
		AggregateType: "subscription", AggregateID: aggregateID, AggregateSequence: sequence,
		PartitionKey: "user:40000000-0000-4000-8000-000000000001", CorrelationID: "50000000-0000-4000-8000-000000000001",
		OccurredAt: time.Now().UTC(), SourceTopic: "subscription.extended.v1", SourcePartition: 0, SourceOffset: offset,
		PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func integrationIntent(notificationID, dedupe string) domain.Intent {
	return domain.Intent{
		NotificationID: notificationID, UserID: "40000000-0000-4000-8000-000000000001",
		NotificationType: "subscription_extended", TemplateVersion: 1, BusinessDedupeKey: dedupe,
		Variables: map[string]string{"period_end": "2026-08-18T12:00:00Z"}, MaxAttempts: 3,
	}
}
