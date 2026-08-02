-- +goose Up
CREATE TABLE subscription_lifecycle_state (
    subscription_id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    last_applied_sequence bigint NOT NULL DEFAULT 0 CHECK (last_applied_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE inbox ADD COLUMN aggregate_sequence bigint NULL CHECK (aggregate_sequence > 0);
CREATE UNIQUE INDEX inbox_lifecycle_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE aggregate_sequence IS NOT NULL;

ALTER TABLE access_credentials
    ADD COLUMN allocation_revision integer NOT NULL DEFAULT 0 CHECK (allocation_revision >= 0),
    ADD COLUMN outbox_sequence bigint NOT NULL DEFAULT 0 CHECK (outbox_sequence >= 0);

ALTER TABLE access_endpoint_snapshots
    ADD COLUMN allocation_revision integer NULL;
UPDATE access_endpoint_snapshots e
SET allocation_revision = GREATEST(
    c.credential_version - CASE WHEN c.status IN ('revoking', 'revoked') THEN 1 ELSE 0 END,
    1
)
FROM access_credentials c
WHERE c.id = e.credential_id;
ALTER TABLE access_endpoint_snapshots
    ALTER COLUMN allocation_revision SET NOT NULL,
    ADD CONSTRAINT access_endpoint_allocation_revision_positive CHECK (allocation_revision > 0);

UPDATE access_credentials c
SET allocation_revision = COALESCE((
    SELECT max(e.allocation_revision)
    FROM access_endpoint_snapshots e
    WHERE e.credential_id = c.id
), 0);

ALTER TABLE access_operations ADD COLUMN allocation_revision integer NULL CHECK (allocation_revision >= 0);
UPDATE access_operations o
SET allocation_revision = c.allocation_revision
FROM access_credentials c
WHERE o.credential_id = c.id AND o.kind = 'revoke';
ALTER TABLE access_operations ADD CONSTRAINT access_operation_allocation_revision_kind CHECK (
    (kind = 'provision' AND allocation_revision IS NULL)
    OR (kind = 'revoke' AND allocation_revision IS NOT NULL)
);

ALTER TABLE outbox ADD COLUMN aggregate_sequence bigint NULL;
WITH ranked AS (
    SELECT event_id, row_number() OVER (PARTITION BY aggregate_id ORDER BY created_at, event_id) AS sequence
    FROM outbox
)
UPDATE outbox o SET aggregate_sequence = ranked.sequence
FROM ranked WHERE ranked.event_id = o.event_id;
ALTER TABLE outbox
    ALTER COLUMN aggregate_sequence SET NOT NULL,
    ADD CONSTRAINT access_outbox_aggregate_sequence_positive CHECK (aggregate_sequence > 0),
    ADD CONSTRAINT access_outbox_aggregate_sequence_unique UNIQUE (aggregate_id, aggregate_sequence);
UPDATE outbox
SET payload = jsonb_set(payload, '{aggregate_sequence}', to_jsonb(aggregate_sequence), true);
UPDATE access_credentials c
SET outbox_sequence = COALESCE((
    SELECT max(o.aggregate_sequence) FROM outbox o WHERE o.aggregate_id = c.id
), 0);
CREATE INDEX access_outbox_aggregate_delivery_idx ON outbox (aggregate_id, aggregate_sequence, state);

CREATE TABLE security_audit_events (
    event_id uuid PRIMARY KEY,
    action text NOT NULL CHECK (action = 'credential_material.read'),
    outcome text NOT NULL CHECK (outcome = 'succeeded'),
    actor_service text NOT NULL CHECK (length(actor_service) BETWEEN 1 AND 64),
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE RESTRICT,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_audit_credential_time_idx
ON security_audit_events (credential_id, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS security_audit_events;
DROP INDEX IF EXISTS access_outbox_aggregate_delivery_idx;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS access_outbox_aggregate_sequence_unique;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS access_outbox_aggregate_sequence_positive;
ALTER TABLE outbox DROP COLUMN IF EXISTS aggregate_sequence;
ALTER TABLE access_operations DROP CONSTRAINT IF EXISTS access_operation_allocation_revision_kind;
ALTER TABLE access_operations DROP COLUMN IF EXISTS allocation_revision;
ALTER TABLE access_endpoint_snapshots DROP CONSTRAINT IF EXISTS access_endpoint_allocation_revision_positive;
ALTER TABLE access_endpoint_snapshots DROP COLUMN IF EXISTS allocation_revision;
ALTER TABLE access_credentials DROP COLUMN IF EXISTS outbox_sequence;
ALTER TABLE access_credentials DROP COLUMN IF EXISTS allocation_revision;
DROP INDEX IF EXISTS inbox_lifecycle_sequence_idx;
ALTER TABLE inbox DROP COLUMN IF EXISTS aggregate_sequence;
DROP TABLE IF EXISTS subscription_lifecycle_state;
