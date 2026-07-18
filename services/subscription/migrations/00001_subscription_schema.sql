-- +goose Up
CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('pending', 'active', 'grace', 'expired', 'suspended', 'revoked')),
    current_period_start timestamptz NULL,
    current_period_end timestamptz NULL,
    grace_ends_at timestamptz NULL,
    next_transition_at timestamptz NULL,
    transition_lease_until timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (current_period_start IS NULL AND current_period_end IS NULL AND grace_ends_at IS NULL)
        OR (current_period_start IS NOT NULL AND current_period_end > current_period_start AND grace_ends_at >= current_period_end)
    )
);
CREATE INDEX subscriptions_due_idx ON subscriptions (next_transition_at)
WHERE next_transition_at IS NOT NULL AND status IN ('pending', 'active', 'grace');

CREATE TABLE subscription_periods (
    id uuid PRIMARY KEY,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    source_order_id uuid NOT NULL UNIQUE,
    source_payment_id uuid NOT NULL UNIQUE,
    plan_id text NOT NULL CHECK (length(plan_id) BETWEEN 1 AND 128),
    region text NOT NULL CHECK (length(region) BETWEEN 2 AND 64),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    duration_days integer NOT NULL CHECK (duration_days BETWEEN 1 AND 3650),
    grace_period_hours integer NOT NULL CHECK (grace_period_hours BETWEEN 0 AND 720),
    paid_at timestamptz NOT NULL,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL,
    grace_ends_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'paid' CHECK (status IN ('paid', 'refunded')),
    refund_id uuid NULL UNIQUE,
    refunded_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end > period_start AND grace_ends_at >= period_end),
    CHECK ((status = 'paid' AND refund_id IS NULL AND refunded_at IS NULL) OR (status = 'refunded' AND refund_id IS NOT NULL AND refunded_at IS NOT NULL))
);
CREATE INDEX subscription_periods_subscription_idx ON subscription_periods (subscription_id, period_start);

-- +goose StatementBegin
CREATE FUNCTION enforce_subscription_period_immutability() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.subscription_id IS DISTINCT FROM OLD.subscription_id
       OR NEW.source_order_id IS DISTINCT FROM OLD.source_order_id
       OR NEW.source_payment_id IS DISTINCT FROM OLD.source_payment_id
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.region IS DISTINCT FROM OLD.region
       OR NEW.amount_minor IS DISTINCT FROM OLD.amount_minor
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.duration_days IS DISTINCT FROM OLD.duration_days
       OR NEW.grace_period_hours IS DISTINCT FROM OLD.grace_period_hours
       OR NEW.paid_at IS DISTINCT FROM OLD.paid_at
       OR NEW.period_start IS DISTINCT FROM OLD.period_start
       OR NEW.period_end IS DISTINCT FROM OLD.period_end
       OR NEW.grace_ends_at IS DISTINCT FROM OLD.grace_ends_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.refund_id IS NOT NULL AND NEW.refund_id IS DISTINCT FROM OLD.refund_id)
       OR (OLD.refunded_at IS NOT NULL AND NEW.refunded_at IS DISTINCT FROM OLD.refunded_at) THEN
        RAISE EXCEPTION 'subscription period snapshot is immutable';
    END IF;
    IF NOT (NEW.status = OLD.status OR (OLD.status = 'paid' AND NEW.status = 'refunded')) THEN
        RAISE EXCEPTION 'invalid subscription period status transition from % to %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER subscription_periods_immutable
BEFORE UPDATE ON subscription_periods
FOR EACH ROW EXECUTE FUNCTION enforce_subscription_period_immutability();

CREATE TABLE inbox (
    event_id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN ('billing.payment.succeeded.v1', 'billing.refund.succeeded.v1')),
    aggregate_id uuid NOT NULL,
    source_payment_id uuid NOT NULL,
    user_id uuid NOT NULL,
    correlation_id uuid NOT NULL,
    payload jsonb NULL CHECK (payload IS NULL OR jsonb_typeof(payload) = 'object'),
    state text NOT NULL CHECK (state IN ('pending', 'processing', 'processed', 'dead')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NULL,
    last_error_code text NULL CHECK (last_error_code IS NULL OR length(last_error_code) <= 64),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz NULL
);
CREATE INDEX inbox_pending_idx ON inbox (next_attempt_at)
WHERE state IN ('pending', 'processing') AND event_type = 'billing.refund.succeeded.v1';

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
CREATE INDEX outbox_pending_idx ON outbox (next_attempt_at)
WHERE state IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS consumer_dead_letters;
DROP TABLE IF EXISTS inbox;
DROP TRIGGER IF EXISTS subscription_periods_immutable ON subscription_periods;
DROP FUNCTION IF EXISTS enforce_subscription_period_immutability();
DROP TABLE IF EXISTS subscription_periods;
DROP TABLE IF EXISTS subscriptions;
