-- +goose Up
CREATE TABLE orders (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('created', 'payment_pending', 'paid', 'canceled')),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    plan_id text NOT NULL CHECK (length(plan_id) BETWEEN 1 AND 128),
    region text NOT NULL CHECK (length(region) BETWEEN 2 AND 64),
    accepted_terms_version text NOT NULL CHECK (length(accepted_terms_version) BETWEEN 1 AND 64),
    plan_snapshot jsonb NOT NULL CHECK (jsonb_typeof(plan_snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX orders_one_open_per_user_idx
ON orders (user_id) WHERE status IN ('created', 'payment_pending');
CREATE INDEX orders_user_created_idx ON orders (user_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION reject_order_snapshot_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.amount_minor IS DISTINCT FROM OLD.amount_minor
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.region IS DISTINCT FROM OLD.region
       OR NEW.accepted_terms_version IS DISTINCT FROM OLD.accepted_terms_version
       OR NEW.plan_snapshot IS DISTINCT FROM OLD.plan_snapshot
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'order financial and product snapshot is immutable';
    END IF;
    IF NOT (
        NEW.status = OLD.status
        OR (OLD.status = 'created' AND NEW.status IN ('payment_pending', 'canceled'))
        OR (OLD.status = 'payment_pending' AND NEW.status IN ('paid', 'canceled'))
    ) THEN
        RAISE EXCEPTION 'invalid order status transition from % to %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER orders_snapshot_immutable
BEFORE UPDATE ON orders
FOR EACH ROW EXECUTE FUNCTION reject_order_snapshot_change();

CREATE TABLE payments (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
    provider text NOT NULL CHECK (provider = 'yookassa'),
    provider_payment_id text NULL UNIQUE CHECK (provider_payment_id IS NULL OR length(provider_payment_id) BETWEEN 1 AND 128),
    provider_idempotency_key text NOT NULL UNIQUE CHECK (length(provider_idempotency_key) BETWEEN 1 AND 64),
    status text NOT NULL CHECK (status IN ('created', 'verification_pending', 'pending', 'succeeded', 'canceled', 'failed')),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    confirmation_url text NULL CHECK (confirmation_url IS NULL OR length(confirmation_url) <= 2048),
    provider_create_deadline timestamptz NOT NULL,
    next_reconcile_at timestamptz NOT NULL DEFAULT now(),
    reconcile_lease_until timestamptz NULL,
    reconcile_attempts integer NOT NULL DEFAULT 0 CHECK (reconcile_attempts >= 0),
    last_error_code text NULL CHECK (last_error_code IS NULL OR length(last_error_code) <= 64),
    paid_at timestamptz NULL,
    canceled_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payments_reconcile_idx ON payments (next_reconcile_at)
WHERE status IN ('created', 'verification_pending', 'pending');

-- +goose StatementBegin
CREATE FUNCTION enforce_payment_invariants() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.order_id IS DISTINCT FROM OLD.order_id
       OR NEW.provider IS DISTINCT FROM OLD.provider
       OR NEW.provider_idempotency_key IS DISTINCT FROM OLD.provider_idempotency_key
       OR NEW.amount_minor IS DISTINCT FROM OLD.amount_minor
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.provider_create_deadline IS DISTINCT FROM OLD.provider_create_deadline
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.provider_payment_id IS NOT NULL AND NEW.provider_payment_id IS DISTINCT FROM OLD.provider_payment_id) THEN
        RAISE EXCEPTION 'payment identity and financial fields are immutable';
    END IF;
    IF NOT (
        NEW.status = OLD.status
        OR (OLD.status = 'created' AND NEW.status IN ('verification_pending', 'pending', 'failed'))
        OR (OLD.status = 'verification_pending' AND NEW.status IN ('pending', 'succeeded', 'canceled', 'failed'))
        OR (OLD.status = 'pending' AND NEW.status IN ('succeeded', 'canceled', 'failed'))
    ) THEN
        RAISE EXCEPTION 'invalid payment status transition from % to %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER payments_invariants
BEFORE UPDATE ON payments
FOR EACH ROW EXECUTE FUNCTION enforce_payment_invariants();

CREATE TABLE idempotency_keys (
    subject_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create_order', 'create_payment')),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_hash char(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    resource_type text NOT NULL CHECK (resource_type IN ('order', 'payment')),
    resource_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_id, operation, idempotency_key)
);

CREATE TABLE webhook_inbox (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider = 'yookassa'),
    event_type text NOT NULL CHECK (event_type IN ('payment.succeeded', 'payment.canceled')),
    provider_object_id text NOT NULL CHECK (length(provider_object_id) BETWEEN 1 AND 128),
    observed_status text NOT NULL CHECK (observed_status IN ('pending', 'succeeded', 'canceled')),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'processing', 'processed', 'dead')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NULL,
    last_error_code text NULL CHECK (last_error_code IS NULL OR length(last_error_code) <= 64),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz NULL,
    UNIQUE (provider, event_type, provider_object_id, observed_status)
);
CREATE INDEX webhook_inbox_pending_idx ON webhook_inbox (next_attempt_at)
WHERE state IN ('pending', 'processing');

CREATE TABLE outbox (
    event_id uuid PRIMARY KEY,
    topic text NOT NULL CHECK (length(topic) BETWEEN 1 AND 249),
    partition_key text NOT NULL CHECK (length(partition_key) BETWEEN 1 AND 128),
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'processing', 'published')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL,
    UNIQUE (topic, aggregate_id)
);
CREATE INDEX outbox_pending_idx ON outbox (next_attempt_at)
WHERE state IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS webhook_inbox;
DROP TABLE IF EXISTS idempotency_keys;
DROP TRIGGER IF EXISTS payments_invariants ON payments;
DROP FUNCTION IF EXISTS enforce_payment_invariants();
DROP TABLE IF EXISTS payments;
DROP TRIGGER IF EXISTS orders_snapshot_immutable ON orders;
DROP FUNCTION IF EXISTS reject_order_snapshot_change();
DROP TABLE IF EXISTS orders;
