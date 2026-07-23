-- +goose Up
ALTER TABLE notification_jobs
    ADD COLUMN source_producer text NULL,
    ADD COLUMN source_aggregate_type text NULL,
    ADD COLUMN source_aggregate_id uuid NULL,
    ADD COLUMN source_aggregate_sequence bigint NULL,
    ADD COLUMN delivery_stream_key text NULL,
    ADD COLUMN delivery_sequence bigint NULL,
    ADD COLUMN supersedes_predecessors boolean NOT NULL DEFAULT false;

UPDATE notification_jobs j
SET source_producer = i.producer,
    source_aggregate_type = i.aggregate_type,
    source_aggregate_id = i.aggregate_id,
    source_aggregate_sequence = i.aggregate_sequence,
    delivery_stream_key = 'legacy:' || j.notification_id::text,
    delivery_sequence = 1
FROM notification_inbox i
WHERE i.event_id = j.source_event_id;

ALTER TABLE notification_jobs
    ALTER COLUMN source_producer SET NOT NULL,
    ALTER COLUMN source_aggregate_type SET NOT NULL,
    ALTER COLUMN source_aggregate_id SET NOT NULL,
    ALTER COLUMN delivery_stream_key SET NOT NULL,
    ALTER COLUMN delivery_sequence SET NOT NULL,
    ADD CONSTRAINT notification_jobs_source_producer_check
        CHECK (length(source_producer) BETWEEN 3 AND 64),
    ADD CONSTRAINT notification_jobs_source_aggregate_type_check
        CHECK (length(source_aggregate_type) BETWEEN 3 AND 64),
    ADD CONSTRAINT notification_jobs_source_sequence_check
        CHECK (source_aggregate_sequence IS NULL OR source_aggregate_sequence > 0),
    ADD CONSTRAINT notification_jobs_delivery_stream_check
        CHECK (length(delivery_stream_key) BETWEEN 3 AND 200),
    ADD CONSTRAINT notification_jobs_delivery_sequence_check
        CHECK (delivery_sequence > 0);

CREATE INDEX notification_jobs_delivery_stream_idx
    ON notification_jobs (delivery_stream_key, delivery_sequence, created_at);

-- +goose Down
DROP INDEX IF EXISTS notification_jobs_delivery_stream_idx;
ALTER TABLE notification_jobs
    DROP CONSTRAINT IF EXISTS notification_jobs_delivery_sequence_check,
    DROP CONSTRAINT IF EXISTS notification_jobs_delivery_stream_check,
    DROP CONSTRAINT IF EXISTS notification_jobs_source_sequence_check,
    DROP CONSTRAINT IF EXISTS notification_jobs_source_aggregate_type_check,
    DROP CONSTRAINT IF EXISTS notification_jobs_source_producer_check,
    DROP COLUMN IF EXISTS supersedes_predecessors,
    DROP COLUMN IF EXISTS delivery_sequence,
    DROP COLUMN IF EXISTS delivery_stream_key,
    DROP COLUMN IF EXISTS source_aggregate_sequence,
    DROP COLUMN IF EXISTS source_aggregate_id,
    DROP COLUMN IF EXISTS source_aggregate_type,
    DROP COLUMN IF EXISTS source_producer;
