package postgres

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationRetentionLegalHoldAndBoundedProcessedWebhookDeletion(t *testing.T) {
	store := openIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := store.pool.Exec(ctx, `
INSERT INTO webhook_inbox (
    id, provider, event_type, provider_object_id, observed_status,
    state, received_at, processed_at
) VALUES
    ('71000000-0000-4000-8000-000000000001','yookassa','payment.succeeded','old-one','succeeded','processed',$1,$1),
    ('71000000-0000-4000-8000-000000000002','yookassa','payment.succeeded','old-two','succeeded','processed',$1,$1),
    ('71000000-0000-4000-8000-000000000003','yookassa','payment.canceled','fresh','canceled','processed',$2,$2),
    ('71000000-0000-4000-8000-000000000004','yookassa','payment.canceled','dead','canceled','dead',$1,NULL)`,
		now.Add(-60*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
INSERT INTO retention_legal_holds (hold_key,scope,reason_code)
VALUES ('legal-review','all_financial_records','legal_review')`); err != nil {
		t.Fatal(err)
	}
	dataset := store.RetentionDatasets(30 * 24 * time.Hour)[0]
	preview, err := dataset.Preview(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Eligible != 0 || preview.Protected != 2 {
		t.Fatalf("preview under hold = %+v, want 0 eligible and 2 protected", preview)
	}
	if deleted, err := dataset.DeleteBatch(ctx, now.Add(-30*24*time.Hour), 10); err != nil || deleted != 0 {
		t.Fatalf("delete under hold = %d, %v", deleted, err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM retention_legal_holds WHERE hold_key='legal-review'`); err != nil {
		t.Fatal(err)
	}
	if deleted, err := dataset.DeleteBatch(ctx, now.Add(-30*24*time.Hour), 1); err != nil || deleted != 1 {
		t.Fatalf("bounded delete = %d, %v", deleted, err)
	}
	var oldProcessed, freshAndDead int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_inbox WHERE state='processed' AND processed_at < $1`, now.Add(-30*24*time.Hour)).Scan(&oldProcessed); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_inbox WHERE provider_object_id IN ('fresh','dead')`).Scan(&freshAndDead); err != nil {
		t.Fatal(err)
	}
	if oldProcessed != 1 || freshAndDead != 2 {
		t.Fatalf("remaining old=%d protected terminal rows=%d", oldProcessed, freshAndDead)
	}
}
