-- +goose Up
CREATE TABLE notification_templates (
    notification_type text NOT NULL CHECK (notification_type ~ '^[a-z][a-z0-9_]{2,63}$'),
    template_version integer NOT NULL CHECK (template_version > 0),
    enabled boolean NOT NULL DEFAULT true,
    max_length integer NOT NULL DEFAULT 4096 CHECK (max_length BETWEEN 1 AND 4096),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (notification_type, template_version)
);

INSERT INTO notification_templates (notification_type, template_version) VALUES
    ('payment_confirmed', 1),
    ('subscription_activated', 1),
    ('subscription_extended', 1),
    ('subscription_grace', 1),
    ('subscription_expired', 1),
    ('subscription_revoked', 1),
    ('refund_confirmed', 1),
    ('access_ready', 1),
    ('access_degraded', 1),
    ('provisioning_failed', 1),
    ('access_physically_revoked', 1);

CREATE TABLE notification_cursors (
    stream_key text PRIMARY KEY CHECK (length(stream_key) BETWEEN 3 AND 200),
    producer text NOT NULL CHECK (length(producer) BETWEEN 3 AND 64),
    aggregate_type text NOT NULL CHECK (length(aggregate_type) BETWEEN 3 AND 64),
    aggregate_id uuid NOT NULL,
    last_sequence bigint NOT NULL CHECK (last_sequence > 0),
    last_event_id uuid NOT NULL,
    last_payload_sha256 char(64) NOT NULL CHECK (last_payload_sha256 ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (producer, aggregate_type, aggregate_id)
);

CREATE TABLE notification_inbox (
    event_id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (length(event_type) BETWEEN 3 AND 128),
    source_topic text NOT NULL CHECK (length(source_topic) BETWEEN 1 AND 249),
    source_partition integer NOT NULL CHECK (source_partition >= 0),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    producer text NOT NULL CHECK (length(producer) BETWEEN 3 AND 64),
    aggregate_type text NOT NULL CHECK (length(aggregate_type) BETWEEN 3 AND 64),
    aggregate_id uuid NOT NULL,
    aggregate_sequence bigint NULL CHECK (aggregate_sequence IS NULL OR aggregate_sequence > 0),
    business_dedupe_key text NOT NULL CHECK (length(business_dedupe_key) BETWEEN 3 AND 200),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_topic, source_partition, source_offset),
    UNIQUE NULLS NOT DISTINCT (aggregate_type, aggregate_id, aggregate_sequence)
);

CREATE TABLE notification_jobs (
    notification_id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    subscription_id uuid NULL,
    credential_id uuid NULL,
    notification_type text NOT NULL,
    template_version integer NOT NULL,
    source_event_id uuid NOT NULL REFERENCES notification_inbox(event_id) ON DELETE RESTRICT,
    business_dedupe_key text NOT NULL UNIQUE CHECK (length(business_dedupe_key) BETWEEN 3 AND 200),
    status text NOT NULL CHECK (status IN ('pending', 'processing', 'retry', 'delivered', 'permanently_failed', 'suppressed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_until timestamptz NULL,
    claim_id uuid NULL,
    delivered_at timestamptz NULL,
    terminal_reason_code text NULL CHECK (terminal_reason_code IS NULL OR terminal_reason_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    correlation_id uuid NOT NULL,
    causation_id uuid NULL,
    variables jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(variables) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (notification_type, template_version) REFERENCES notification_templates(notification_type, template_version),
    CHECK ((status = 'processing') = (lease_until IS NOT NULL AND claim_id IS NOT NULL)),
    CHECK ((status = 'delivered') = (delivered_at IS NOT NULL))
);

CREATE INDEX notification_jobs_due_idx ON notification_jobs (next_attempt_at, created_at)
WHERE status IN ('pending', 'retry', 'processing');
CREATE INDEX notification_jobs_user_idx ON notification_jobs (user_id, created_at DESC);

CREATE TABLE notification_dead_letters (
    source_topic text NOT NULL CHECK (length(source_topic) BETWEEN 1 AND 249),
    source_partition integer NOT NULL CHECK (source_partition >= 0),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    event_type text NULL CHECK (event_type IS NULL OR length(event_type) BETWEEN 3 AND 128),
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    state text NOT NULL DEFAULT 'available' CHECK (state IN ('available', 'replay_requested', 'replayed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source_topic, source_partition, source_offset)
);

CREATE TABLE notification_admin_requests (
    idempotency_key text PRIMARY KEY CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_sha256 char(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    notification_id uuid NOT NULL REFERENCES notification_jobs(notification_id) ON DELETE RESTRICT,
    result_status text NOT NULL CHECK (result_status IN ('accepted', 'replayed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE IF EXISTS notification_admin_requests;
DROP TABLE IF EXISTS notification_dead_letters;
DROP TABLE IF EXISTS notification_jobs;
DROP TABLE IF EXISTS notification_inbox;
DROP TABLE IF EXISTS notification_cursors;
DROP TABLE IF EXISTS notification_templates;
