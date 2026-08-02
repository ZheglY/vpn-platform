-- +goose Up
ALTER TABLE subscriptions
    ADD COLUMN aggregate_sequence bigint NOT NULL DEFAULT 0 CHECK (aggregate_sequence >= 0);

ALTER TABLE inbox
    ADD COLUMN source_topic text NULL,
    ADD COLUMN source_partition integer NULL,
    ADD COLUMN source_offset bigint NULL,
    ADD COLUMN payload_sha256 char(64) NULL,
    ADD CONSTRAINT inbox_source_metadata_complete CHECK (
        (source_topic IS NULL AND source_partition IS NULL AND source_offset IS NULL AND payload_sha256 IS NULL)
        OR
        (source_topic IS NOT NULL AND source_partition IS NOT NULL AND source_offset IS NOT NULL AND payload_sha256 IS NOT NULL
         AND source_topic = event_type AND length(source_topic) BETWEEN 1 AND 249
         AND source_partition >= 0 AND source_offset >= 0 AND payload_sha256 ~ '^[0-9a-f]{64}$')
    );
CREATE UNIQUE INDEX inbox_source_record_idx ON inbox (source_topic, source_partition, source_offset)
WHERE source_topic IS NOT NULL;

ALTER TABLE outbox ADD COLUMN aggregate_sequence bigint NULL;
WITH ranked AS (
    SELECT event_id, row_number() OVER (PARTITION BY aggregate_id ORDER BY created_at, event_id) AS sequence
    FROM outbox
)
UPDATE outbox o SET aggregate_sequence = ranked.sequence
FROM ranked WHERE ranked.event_id = o.event_id;
ALTER TABLE outbox
    ALTER COLUMN aggregate_sequence SET NOT NULL,
    ADD CONSTRAINT outbox_aggregate_sequence_positive CHECK (aggregate_sequence > 0),
    ADD CONSTRAINT outbox_aggregate_sequence_unique UNIQUE (aggregate_id, aggregate_sequence);
UPDATE subscriptions s
SET aggregate_sequence = COALESCE((SELECT max(o.aggregate_sequence) FROM outbox o WHERE o.aggregate_id = s.id), 0);
CREATE INDEX outbox_aggregate_delivery_idx ON outbox (aggregate_id, aggregate_sequence, state);

-- +goose Down
DROP INDEX IF EXISTS outbox_aggregate_delivery_idx;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_aggregate_sequence_unique;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_aggregate_sequence_positive;
ALTER TABLE outbox DROP COLUMN IF EXISTS aggregate_sequence;
DROP INDEX IF EXISTS inbox_source_record_idx;
ALTER TABLE inbox DROP CONSTRAINT IF EXISTS inbox_source_metadata_complete;
ALTER TABLE inbox DROP COLUMN IF EXISTS payload_sha256;
ALTER TABLE inbox DROP COLUMN IF EXISTS source_offset;
ALTER TABLE inbox DROP COLUMN IF EXISTS source_partition;
ALTER TABLE inbox DROP COLUMN IF EXISTS source_topic;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS aggregate_sequence;
