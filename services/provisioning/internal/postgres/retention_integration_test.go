package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIntegrationRetentionKeepsUnreplayedDLQAndFreshHealth(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.SeedNodes(ctx, testNodeSeeds(10)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
INSERT INTO node_health_snapshots (node_id,observed_at,config_revision,active_clients,xray_healthy,agent_version,xray_version) VALUES
('61000000-0000-4000-8000-000000000001',$1,1,0,true,'1.0.0','1.0.0'),
('61000000-0000-4000-8000-000000000001',$2,1,0,true,'1.0.0','1.0.0')`,
		now.Add(-60*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
INSERT INTO consumer_dead_letters (
    source_topic,source_partition,source_offset,payload_sha256,reason_code,replay_state,first_seen_at,replayed_at
) VALUES
('access.provision.request.v1',0,901,$2,'invalid_event_data','replayed',$1,$1),
('access.provision.request.v1',0,902,$2,'invalid_event_data','available',$1,NULL)`,
		now.Add(-60*24*time.Hour), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	datasets := store.RetentionDatasets(30 * 24 * time.Hour)
	for _, dataset := range datasets {
		if deleted, err := dataset.DeleteBatch(ctx, now.Add(-30*24*time.Hour), 10); err != nil || deleted != 1 {
			t.Fatalf("%s delete = %d, %v", dataset.Name, deleted, err)
		}
	}
	var freshHealth, availableDLQ int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM node_health_snapshots`).Scan(&freshHealth); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM consumer_dead_letters WHERE replay_state='available'`).Scan(&availableDLQ); err != nil {
		t.Fatal(err)
	}
	if freshHealth != 1 || availableDLQ != 1 {
		t.Fatalf("fresh health=%d available DLQ=%d, want both 1", freshHealth, availableDLQ)
	}
}
