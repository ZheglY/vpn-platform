-- +goose Up
CREATE TABLE nodes (
    id uuid PRIMARY KEY,
    region text NOT NULL CHECK (length(region) BETWEEN 2 AND 64),
    management_url text NOT NULL UNIQUE CHECK (management_url ~ '^https://'),
    management_spiffe_id text NOT NULL UNIQUE CHECK (management_spiffe_id ~ '^spiffe://'),
    status text NOT NULL CHECK (status IN ('active', 'draining', 'offline')),
    capacity_limit integer NOT NULL CHECK (capacity_limit BETWEEN 1 AND 1000000),
    reserve_percent integer NOT NULL DEFAULT 20 CHECK (reserve_percent BETWEEN 20 AND 90),
    allocated_clients integer NOT NULL DEFAULT 0 CHECK (allocated_clients >= 0 AND allocated_clients <= capacity_limit),
    public_address text NOT NULL CHECK (length(public_address) BETWEEN 1 AND 255),
    public_port integer NOT NULL CHECK (public_port BETWEEN 1 AND 65535),
    server_name text NOT NULL CHECK (length(server_name) BETWEEN 1 AND 255),
    reality_public_key char(43) NOT NULL CHECK (reality_public_key ~ '^[A-Za-z0-9_-]{43}$'),
    short_id text NOT NULL CHECK (short_id ~ '^(?:[0-9a-f]{2}){0,8}$'),
    spider_x text NOT NULL DEFAULT '' CHECK (length(spider_x) <= 255),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    agent_version text NULL CHECK (agent_version IS NULL OR length(agent_version) <= 64),
    xray_version text NULL CHECK (xray_version IS NULL OR length(xray_version) <= 64),
    config_revision bigint NOT NULL DEFAULT 0 CHECK (config_revision >= 0),
    last_seen_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX nodes_placement_idx ON nodes (region, status, allocated_clients, id);

CREATE TABLE credential_command_cursors (
    credential_id uuid PRIMARY KEY,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    last_desired_revision integer NOT NULL DEFAULT 0 CHECK (last_desired_revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE command_inbox (
    event_id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN ('access.provision.request.v1', 'access.revoke.request.v1')),
    credential_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    aggregate_sequence bigint NOT NULL CHECK (aggregate_sequence > 0),
    desired_revision integer NOT NULL CHECK (desired_revision > 0),
    correlation_id uuid NOT NULL,
    source_topic text NOT NULL CHECK (source_topic = event_type),
    source_partition integer NOT NULL CHECK (source_partition >= 0),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    processed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (credential_id, aggregate_sequence),
    UNIQUE (source_topic, source_partition, source_offset)
);

CREATE TABLE operations (
    id uuid PRIMARY KEY,
    credential_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('provision', 'revoke')),
    desired_revision integer NOT NULL CHECK (desired_revision > 0),
    command_sequence bigint NOT NULL CHECK (command_sequence > 0),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'processing', 'retry', 'succeeded', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 32),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NULL,
    allocation_revision integer NOT NULL DEFAULT 0 CHECK (allocation_revision >= 0),
    last_error_code text NULL CHECK (last_error_code IS NULL OR length(last_error_code) <= 64),
    correlation_id uuid NOT NULL,
    causation_event_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz NULL,
    UNIQUE (credential_id, command_sequence),
    UNIQUE (credential_id, desired_revision)
);
CREATE INDEX operations_due_idx ON operations (next_attempt_at, created_at)
WHERE state IN ('pending', 'retry', 'processing');

CREATE TABLE allocations (
    id uuid PRIMARY KEY,
    credential_id uuid NOT NULL,
    operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
	desired_operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    role text NOT NULL CHECK (role IN ('primary', 'failover')),
    protocol text NOT NULL CHECK (protocol = 'vless_reality'),
    desired_revision integer NOT NULL CHECK (desired_revision > 0),
	desired_state text NOT NULL DEFAULT 'present' CHECK (desired_state IN ('present', 'absent')),
    allocation_revision integer NOT NULL CHECK (allocation_revision > 0),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'active', 'failed', 'revoked')),
    applied_config_revision bigint NULL CHECK (applied_config_revision IS NULL OR applied_config_revision > 0),
    last_error_code text NULL CHECK (last_error_code IS NULL OR length(last_error_code) <= 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz NULL,
    revoked_at timestamptz NULL,
    UNIQUE (credential_id, node_id),
    UNIQUE (credential_id, role)
);
CREATE INDEX allocations_node_active_idx ON allocations (node_id, state)
WHERE state <> 'revoked';

CREATE TABLE node_health_snapshots (
    id bigserial PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
    observed_at timestamptz NOT NULL DEFAULT now(),
    config_revision bigint NOT NULL CHECK (config_revision >= 0),
    active_clients integer NOT NULL CHECK (active_clients >= 0),
    xray_healthy boolean NOT NULL,
    agent_version text NOT NULL CHECK (length(agent_version) BETWEEN 1 AND 64),
    xray_version text NOT NULL CHECK (length(xray_version) BETWEEN 1 AND 64)
);
CREATE INDEX node_health_recent_idx ON node_health_snapshots (node_id, observed_at DESC);

CREATE TABLE consumer_dead_letters (
    source_topic text NOT NULL,
    source_partition integer NOT NULL CHECK (source_partition >= 0),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    payload_sha256 char(64) NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    reason_code text NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 64),
    replay_state text NOT NULL DEFAULT 'available' CHECK (replay_state IN ('available', 'requested', 'replayed')),
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    replayed_at timestamptz NULL,
    PRIMARY KEY (source_topic, source_partition, source_offset)
);

CREATE TABLE outbox (
    event_id uuid PRIMARY KEY,
    topic text NOT NULL CHECK (length(topic) BETWEEN 1 AND 249),
    partition_key text NOT NULL CHECK (length(partition_key) BETWEEN 1 AND 192),
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
CREATE INDEX provisioning_outbox_pending_idx ON outbox (next_attempt_at, created_at)
WHERE state IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS consumer_dead_letters;
DROP TABLE IF EXISTS node_health_snapshots;
DROP TABLE IF EXISTS allocations;
DROP TABLE IF EXISTS operations;
DROP TABLE IF EXISTS command_inbox;
DROP TABLE IF EXISTS credential_command_cursors;
DROP TABLE IF EXISTS nodes;
