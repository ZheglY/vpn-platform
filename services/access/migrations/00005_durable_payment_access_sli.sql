-- +goose Up
CREATE TABLE payment_access_sli (
    payment_id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    paid_at timestamptz NULL,
    payment_event_id uuid NULL UNIQUE,
    subscription_id uuid NULL,
    fulfillment_kind text NULL CHECK (fulfillment_kind IN ('activation', 'extension')),
    fulfillment_event_id uuid NULL UNIQUE,
    fulfilled_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (fulfillment_event_id IS NULL AND subscription_id IS NULL AND fulfillment_kind IS NULL AND fulfilled_at IS NULL)
        OR
        (fulfillment_event_id IS NOT NULL AND subscription_id IS NOT NULL AND fulfillment_kind IS NOT NULL AND fulfilled_at IS NOT NULL)
    ),
    CHECK (payment_event_id IS NOT NULL OR paid_at IS NULL)
);
CREATE INDEX payment_access_sli_deadline_idx
ON payment_access_sli (paid_at)
WHERE fulfilled_at IS NULL;

ALTER TABLE inbox DROP CONSTRAINT inbox_event_type_check;
ALTER TABLE inbox ADD CONSTRAINT inbox_event_type_check CHECK (event_type IN (
    'billing.payment.succeeded.v1',
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.grace.started.v1', 'subscription.expired.v1', 'subscription.revoked.v1',
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
));

-- +goose Down
ALTER TABLE inbox DROP CONSTRAINT inbox_event_type_check;
ALTER TABLE inbox ADD CONSTRAINT inbox_event_type_check CHECK (event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.grace.started.v1', 'subscription.expired.v1', 'subscription.revoked.v1',
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
));
DROP TABLE IF EXISTS payment_access_sli;
