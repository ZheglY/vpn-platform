-- +goose Up
CREATE TABLE access_credentials (
    id uuid PRIMARY KEY,
    subscription_id uuid NOT NULL,
    user_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('provisioning', 'active', 'degraded', 'failed', 'revoking', 'revoked')),
    credential_version integer NOT NULL CHECK (credential_version > 0),
    vless_uuid_ciphertext bytea NOT NULL CHECK (octet_length(vless_uuid_ciphertext) >= 29),
    encryption_key_version integer NOT NULL CHECK (encryption_key_version > 0),
    entitlement_expires_at timestamptz NOT NULL,
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'revoked' AND revoked_at IS NOT NULL) OR (status <> 'revoked' AND revoked_at IS NULL))
);
CREATE UNIQUE INDEX access_credentials_current_subscription_idx
ON access_credentials (subscription_id)
WHERE status <> 'revoked';
CREATE INDEX access_credentials_user_idx ON access_credentials (user_id);

CREATE TABLE access_endpoint_snapshots (
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE CASCADE,
    node_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('primary', 'failover')),
    address text NOT NULL CHECK (length(address) BETWEEN 1 AND 255),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    server_name text NOT NULL CHECK (length(server_name) BETWEEN 1 AND 255),
    reality_public_key text NOT NULL CHECK (reality_public_key ~ '^[A-Za-z0-9_-]{43}$'),
    short_id text NOT NULL CHECK (short_id ~ '^[0-9a-fA-F]{0,16}$' AND mod(length(short_id), 2) = 0),
    spider_x text NOT NULL DEFAULT '' CHECK (length(spider_x) <= 255),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (credential_id, node_id),
    UNIQUE (credential_id, role)
);

CREATE TABLE access_operations (
    id uuid PRIMARY KEY,
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('provision', 'revoke')),
    desired_revision integer NOT NULL CHECK (desired_revision > 0),
    status text NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    causation_event_id uuid NOT NULL,
    failure_code text NULL CHECK (failure_code IS NULL OR length(failure_code) BETWEEN 1 AND 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz NULL,
    UNIQUE (credential_id, kind, desired_revision),
    UNIQUE (causation_event_id, kind)
);

CREATE TABLE subscription_tokens (
    id uuid PRIMARY KEY,
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE RESTRICT,
    token_lookup_hmac bytea NOT NULL UNIQUE CHECK (octet_length(token_lookup_hmac) = 32),
    status text NOT NULL CHECK (status IN ('active', 'rotated', 'revoked', 'expired')),
    expires_at timestamptz NOT NULL,
    rotated_from uuid NULL REFERENCES subscription_tokens(id) ON DELETE RESTRICT,
    last_used_at timestamptz NULL,
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'active' AND revoked_at IS NULL) OR (status <> 'active' AND revoked_at IS NOT NULL))
);
CREATE UNIQUE INDEX subscription_tokens_active_credential_idx
ON subscription_tokens (credential_id)
WHERE status = 'active';

CREATE TABLE url_idempotency (
    idempotency_key text PRIMARY KEY CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    subscription_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('issue', 'rotate')),
    request_sha256 char(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    token_id uuid NOT NULL REFERENCES subscription_tokens(id) ON DELETE RESTRICT,
    completed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE inbox (
    event_id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN (
        'subscription.activated.v1', 'subscription.extended.v1',
        'subscription.expired.v1', 'subscription.revoked.v1',
        'access.provision.succeeded.v1', 'access.provision.failed.v1',
        'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
    )),
    aggregate_id uuid NOT NULL,
    correlation_id uuid NOT NULL,
    source_topic text NOT NULL CHECK (source_topic = event_type AND length(source_topic) BETWEEN 1 AND 249),
    source_partition integer NOT NULL CHECK (source_partition >= 0),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_topic, source_partition, source_offset)
);

CREATE TABLE consumer_dead_letters (
    topic text NOT NULL,
    partition integer NOT NULL,
    record_offset bigint NOT NULL,
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    reason_code text NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 64),
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (topic, partition, record_offset)
);

CREATE TABLE outbox (
    event_id uuid PRIMARY KEY,
    topic text NOT NULL CHECK (length(topic) BETWEEN 1 AND 249),
    partition_key text NOT NULL CHECK (length(partition_key) BETWEEN 1 AND 128),
    aggregate_id uuid NOT NULL,
    dedupe_key text NOT NULL UNIQUE CHECK (length(dedupe_key) BETWEEN 1 AND 256),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'processing', 'published')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL
);
CREATE INDEX access_outbox_pending_idx ON outbox (next_attempt_at, created_at)
WHERE state IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS consumer_dead_letters;
DROP TABLE IF EXISTS inbox;
DROP TABLE IF EXISTS url_idempotency;
DROP TABLE IF EXISTS subscription_tokens;
DROP TABLE IF EXISTS access_operations;
DROP TABLE IF EXISTS access_endpoint_snapshots;
DROP TABLE IF EXISTS access_credentials;
