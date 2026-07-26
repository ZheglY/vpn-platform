package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
)

func (s *Store) RetentionClock(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read provisioning retention clock: %w", err)
	}
	return now.UTC(), nil
}

func (s *Store) RetentionDatasets(keepFor time.Duration) []retention.Dataset {
	return []retention.Dataset{
		{
			Name:    "node_health_snapshots",
			KeepFor: keepFor,
			Preview: func(ctx context.Context, cutoff time.Time) (retention.Eligibility, error) {
				return previewCount(ctx, s, `SELECT count(*) FROM node_health_snapshots WHERE observed_at < $1`, cutoff)
			},
			DeleteBatch: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteRetentionBatch(ctx, s, `
WITH selected AS (
    SELECT id FROM node_health_snapshots
    WHERE observed_at < $1
    ORDER BY observed_at, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM node_health_snapshots item USING selected
WHERE item.id = selected.id`, cutoff, limit)
			},
		},
		{
			Name:    "replayed_dead_letters",
			KeepFor: keepFor,
			Preview: func(ctx context.Context, cutoff time.Time) (retention.Eligibility, error) {
				return previewCount(ctx, s, `SELECT count(*) FROM consumer_dead_letters WHERE replay_state = 'replayed' AND replayed_at < $1`, cutoff)
			},
			DeleteBatch: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return deleteRetentionBatch(ctx, s, `
WITH selected AS (
    SELECT source_topic, source_partition, source_offset
    FROM consumer_dead_letters
    WHERE replay_state = 'replayed' AND replayed_at < $1
    ORDER BY replayed_at, source_topic, source_partition, source_offset
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM consumer_dead_letters item USING selected
WHERE item.source_topic = selected.source_topic
  AND item.source_partition = selected.source_partition
  AND item.source_offset = selected.source_offset`, cutoff, limit)
			},
		},
	}
}

func previewCount(ctx context.Context, store *Store, query string, cutoff time.Time) (retention.Eligibility, error) {
	var count int64
	if err := store.pool.QueryRow(ctx, query, cutoff).Scan(&count); err != nil {
		return retention.Eligibility{}, fmt.Errorf("preview provisioning retention: %w", err)
	}
	return retention.Eligibility{Eligible: count}, nil
}

func deleteRetentionBatch(ctx context.Context, store *Store, query string, cutoff time.Time, limit int) (int64, error) {
	tag, err := store.pool.Exec(ctx, query, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete provisioning retention batch: %w", err)
	}
	return tag.RowsAffected(), nil
}
