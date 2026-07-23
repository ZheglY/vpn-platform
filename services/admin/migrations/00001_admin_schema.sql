-- +goose Up
CREATE TABLE admin_principals (
    spiffe_id text PRIMARY KEY CHECK (spiffe_id ~ '^spiffe://[A-Za-z0-9.-]+/ns/[A-Za-z0-9._-]+/admin/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE admin_role_grants (
    spiffe_id text NOT NULL REFERENCES admin_principals(spiffe_id) ON DELETE RESTRICT,
    role text NOT NULL CHECK (role IN ('support_readonly', 'operations', 'security', 'finance_readonly')),
    granted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (spiffe_id, role)
);

CREATE TABLE admin_action_requests (
    action_id uuid PRIMARY KEY,
    actor_spiffe_id text NOT NULL REFERENCES admin_principals(spiffe_id) ON DELETE RESTRICT,
    actor_principal text NOT NULL CHECK (length(actor_principal) BETWEEN 1 AND 64),
    action text NOT NULL CHECK (action IN ('notification.retry', 'subscription.revoke', 'access.provisioning.recover')),
    permission text NOT NULL CHECK (length(permission) BETWEEN 3 AND 64),
    target_type text NOT NULL CHECK (target_type IN ('notification', 'subscription', 'credential')),
    target_id uuid NOT NULL,
    reason text NOT NULL CHECK (length(reason) BETWEEN 3 AND 512),
    reason_code text NULL CHECK (reason_code IS NULL OR length(reason_code) BETWEEN 3 AND 64),
    idempotency_key_sha256 char(64) NOT NULL CHECK (idempotency_key_sha256 ~ '^[0-9a-f]{64}$'),
    request_sha256 char(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    correlation_id uuid NOT NULL,
    roles_snapshot text[] NOT NULL CHECK (cardinality(roles_snapshot) BETWEEN 1 AND 4),
    permissions_snapshot text[] NOT NULL CHECK (cardinality(permissions_snapshot) BETWEEN 1 AND 32),
    status text NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    result jsonb NULL CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
    error_code text NULL CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz NULL,
    UNIQUE (actor_spiffe_id, action, idempotency_key_sha256),
    CHECK ((status = 'pending' AND completed_at IS NULL AND result IS NULL AND error_code IS NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL AND result IS NOT NULL AND error_code IS NULL)
        OR (status = 'failed' AND completed_at IS NOT NULL AND result IS NULL AND error_code IS NOT NULL))
);
CREATE INDEX admin_actions_target_idx ON admin_action_requests (target_type, target_id, created_at DESC);

CREATE TABLE admin_audit_events (
    audit_event_id uuid PRIMARY KEY,
    action_id uuid NOT NULL REFERENCES admin_action_requests(action_id) ON DELETE RESTRICT,
    actor_principal text NOT NULL CHECK (length(actor_principal) BETWEEN 1 AND 64),
    verified_spiffe_identity text NOT NULL REFERENCES admin_principals(spiffe_id) ON DELETE RESTRICT,
    roles_snapshot text[] NOT NULL,
    permissions_snapshot text[] NOT NULL,
    action text NOT NULL,
    permission text NOT NULL,
    target_type text NOT NULL,
    target_id uuid NOT NULL,
    reason text NOT NULL CHECK (length(reason) BETWEEN 3 AND 512),
    idempotency_key_sha256 char(64) NOT NULL CHECK (idempotency_key_sha256 ~ '^[0-9a-f]{64}$'),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    correlation_id uuid NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('accepted', 'succeeded', 'failed')),
    error_code text NULL CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz NULL,
    CHECK ((outcome = 'accepted' AND completed_at IS NULL AND error_code IS NULL)
        OR (outcome = 'succeeded' AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (outcome = 'failed' AND completed_at IS NOT NULL AND error_code IS NOT NULL))
);
CREATE INDEX admin_audit_time_idx ON admin_audit_events (occurred_at DESC, audit_event_id DESC);
CREATE INDEX admin_audit_target_idx ON admin_audit_events (target_type, target_id, occurred_at DESC);

-- +goose StatementBegin
CREATE FUNCTION reject_admin_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'admin audit is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER admin_audit_no_update
BEFORE UPDATE ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION reject_admin_audit_mutation();
CREATE TRIGGER admin_audit_no_delete
BEFORE DELETE ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION reject_admin_audit_mutation();

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'admin_app') THEN
        GRANT USAGE ON SCHEMA public TO admin_app;
        GRANT SELECT ON admin_principals, admin_role_grants, admin_action_requests, admin_audit_events TO admin_app;
        GRANT INSERT, UPDATE ON admin_action_requests TO admin_app;
        GRANT INSERT ON admin_audit_events TO admin_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON admin_audit_events FROM admin_app;
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON admin_principals, admin_role_grants FROM admin_app;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS admin_audit_no_delete ON admin_audit_events;
DROP TRIGGER IF EXISTS admin_audit_no_update ON admin_audit_events;
DROP FUNCTION IF EXISTS reject_admin_audit_mutation();
DROP TABLE IF EXISTS admin_audit_events;
DROP TABLE IF EXISTS admin_action_requests;
DROP TABLE IF EXISTS admin_role_grants;
DROP TABLE IF EXISTS admin_principals;
