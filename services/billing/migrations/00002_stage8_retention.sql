-- +goose Up
CREATE TABLE retention_legal_holds (
    hold_key text PRIMARY KEY CHECK (hold_key ~ '^[a-z][a-z0-9_-]{2,63}$'),
    scope text NOT NULL CHECK (scope IN ('all_financial_records', 'webhook_inbox')),
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{2,63}$'),
    active_until timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (active_until IS NULL OR active_until > created_at)
);
CREATE INDEX retention_legal_holds_active_idx
ON retention_legal_holds (scope, active_until);

-- Payment, refund, order, provider-attempt, idempotency, and outbox records are
-- intentionally outside every automatic retention query.

-- +goose Down
DROP TABLE IF EXISTS retention_legal_holds;
