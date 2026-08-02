package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIntegrationRetentionDeletesOnlyReplayedNotificationDLQ(t *testing.T) {
	store := notificationIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := store.pool.Exec(ctx, `
INSERT INTO notification_dead_letters (
    source_topic,source_partition,source_offset,payload_sha256,event_type,reason_code,state,created_at,updated_at
) VALUES
('subscription.extended.v1',0,801,$1,'subscription.extended.v1','invalid_event_data','replayed',$2,$2),
('subscription.extended.v1',0,802,$1,'subscription.extended.v1','invalid_event_data','available',$2,$2)`,
		strings.Repeat("a", 64), now.Add(-60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	dataset := store.RetentionDatasets(30 * 24 * time.Hour)[0]
	if deleted, err := dataset.DeleteBatch(ctx, now.Add(-30*24*time.Hour), 10); err != nil || deleted != 1 {
		t.Fatalf("delete = %d, %v", deleted, err)
	}
	var remaining int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM notification_dead_letters WHERE state='available'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining available DLQ = %d, want 1", remaining)
	}
}
