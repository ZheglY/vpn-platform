-- +goose Up
ALTER TABLE access_credentials
    ADD COLUMN revocation_reason text NULL CHECK (revocation_reason IN (
        'expired', 'refund', 'refund_gap', 'admin_block', 'abuse', 'deleted'
    )),
    ADD COLUMN notification_sequence bigint NOT NULL DEFAULT 0 CHECK (notification_sequence >= 0);

WITH ranked AS (
    SELECT event_id,
           row_number() OVER (PARTITION BY aggregate_id ORDER BY created_at, event_id) AS notification_sequence
    FROM outbox
    WHERE topic = 'access.ready.v1'
)
UPDATE outbox o
SET payload = jsonb_set(o.payload, '{aggregate_sequence}', to_jsonb(ranked.notification_sequence), true)
FROM ranked WHERE ranked.event_id = o.event_id;

UPDATE access_credentials c
SET notification_sequence = COALESCE((
    SELECT count(*) FROM outbox o
    WHERE o.aggregate_id = c.id AND o.topic = 'access.ready.v1'
), 0);

UPDATE access_credentials
SET revocation_reason = 'expired'
WHERE status IN ('revoking', 'revoked');

ALTER TABLE access_credentials
    ADD CONSTRAINT access_credentials_revocation_reason_state CHECK (
        (status IN ('revoking', 'revoked') AND revocation_reason IS NOT NULL)
        OR (status NOT IN ('revoking', 'revoked') AND revocation_reason IS NULL)
    );

ALTER TABLE inbox DROP CONSTRAINT inbox_ordered_event_sequence_required;
ALTER TABLE inbox DROP CONSTRAINT inbox_event_type_check;
ALTER TABLE inbox ADD CONSTRAINT inbox_event_type_check CHECK (event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.grace.started.v1', 'subscription.expired.v1', 'subscription.revoked.v1',
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
));
ALTER TABLE inbox ADD CONSTRAINT inbox_ordered_event_sequence_required CHECK (
    aggregate_sequence IS NOT NULL
);

DROP INDEX inbox_lifecycle_sequence_idx;
CREATE UNIQUE INDEX inbox_lifecycle_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.grace.started.v1', 'subscription.expired.v1', 'subscription.revoked.v1'
);

CREATE TABLE admin_recovery_requests (
    idempotency_key text PRIMARY KEY CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_sha256 char(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    action_id uuid NOT NULL UNIQUE,
    correlation_id uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE RESTRICT,
    operation_id uuid NOT NULL REFERENCES access_operations(id) ON DELETE RESTRICT,
    desired_revision integer NOT NULL CHECK (desired_revision > 1),
    result_status text NOT NULL CHECK (result_status = 'provisioning'),
    completed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE IF EXISTS admin_recovery_requests;
DROP INDEX inbox_lifecycle_sequence_idx;
CREATE UNIQUE INDEX inbox_lifecycle_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.expired.v1', 'subscription.revoked.v1'
);
ALTER TABLE inbox DROP CONSTRAINT inbox_ordered_event_sequence_required;
ALTER TABLE inbox DROP CONSTRAINT inbox_event_type_check;
ALTER TABLE inbox ADD CONSTRAINT inbox_event_type_check CHECK (event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.expired.v1', 'subscription.revoked.v1',
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
));
ALTER TABLE inbox ADD CONSTRAINT inbox_ordered_event_sequence_required CHECK (
    aggregate_sequence IS NOT NULL
);
ALTER TABLE access_credentials DROP CONSTRAINT access_credentials_revocation_reason_state;
ALTER TABLE access_credentials DROP COLUMN notification_sequence;
ALTER TABLE access_credentials DROP COLUMN revocation_reason;
