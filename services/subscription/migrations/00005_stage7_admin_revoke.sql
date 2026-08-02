-- +goose Up
CREATE TABLE admin_revoke_requests (
    idempotency_key text PRIMARY KEY CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_sha256 char(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    action_id uuid NOT NULL UNIQUE,
    correlation_id uuid NOT NULL,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    reason_code text NOT NULL CHECK (reason_code IN ('admin_block', 'abuse', 'deleted')),
    result_status text NOT NULL CHECK (result_status = 'revoked'),
    completed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE IF EXISTS admin_revoke_requests;
