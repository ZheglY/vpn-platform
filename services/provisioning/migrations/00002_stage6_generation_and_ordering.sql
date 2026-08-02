-- +goose Up
ALTER TABLE operations DROP CONSTRAINT operations_state_check;
ALTER TABLE operations ADD CONSTRAINT operations_state_check
CHECK (state IN ('pending', 'processing', 'retry', 'succeeded', 'failed', 'superseded'));

CREATE TABLE credential_outcome_cursors (
    credential_id uuid PRIMARY KEY,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE outbox ADD COLUMN aggregate_sequence bigint NULL CHECK (aggregate_sequence > 0);
WITH ranked AS (
    SELECT event_id,
           row_number() OVER (PARTITION BY aggregate_id ORDER BY created_at, event_id) AS sequence
    FROM outbox
    WHERE topic IN (
        'access.provision.succeeded.v1', 'access.provision.failed.v1',
        'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
    )
)
UPDATE outbox o
SET aggregate_sequence = ranked.sequence,
    payload = jsonb_set(o.payload, '{aggregate_sequence}', to_jsonb(ranked.sequence), true)
FROM ranked
WHERE o.event_id = ranked.event_id;

INSERT INTO credential_outcome_cursors (credential_id, last_sequence)
SELECT aggregate_id, max(aggregate_sequence)
FROM outbox
WHERE aggregate_sequence IS NOT NULL
GROUP BY aggregate_id;

ALTER TABLE outbox ADD CONSTRAINT provisioning_outbox_outcome_sequence_required CHECK (
    (topic IN (
        'access.provision.succeeded.v1', 'access.provision.failed.v1',
        'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
    )) = (aggregate_sequence IS NOT NULL)
);
CREATE UNIQUE INDEX provisioning_outbox_aggregate_sequence_idx
ON outbox (aggregate_id, aggregate_sequence)
WHERE aggregate_sequence IS NOT NULL;
CREATE INDEX provisioning_outbox_aggregate_delivery_idx
ON outbox (aggregate_id, aggregate_sequence, state)
WHERE aggregate_sequence IS NOT NULL;

ALTER TABLE allocations
    ADD COLUMN next_reconcile_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN reconcile_lease_until timestamptz NULL,
    ADD COLUMN reconcile_claim_id uuid NULL,
    ADD COLUMN reconcile_attempts integer NOT NULL DEFAULT 0 CHECK (reconcile_attempts >= 0);
CREATE INDEX allocations_reconciliation_due_idx
ON allocations (next_reconcile_at, created_at, id);

-- +goose Down
DROP INDEX IF EXISTS allocations_reconciliation_due_idx;
ALTER TABLE allocations
    DROP COLUMN IF EXISTS reconcile_attempts,
    DROP COLUMN IF EXISTS reconcile_claim_id,
    DROP COLUMN IF EXISTS reconcile_lease_until,
    DROP COLUMN IF EXISTS next_reconcile_at;
DROP INDEX IF EXISTS provisioning_outbox_aggregate_delivery_idx;
DROP INDEX IF EXISTS provisioning_outbox_aggregate_sequence_idx;
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS provisioning_outbox_outcome_sequence_required;
ALTER TABLE outbox DROP COLUMN IF EXISTS aggregate_sequence;
DROP TABLE IF EXISTS credential_outcome_cursors;
ALTER TABLE operations DROP CONSTRAINT operations_state_check;
ALTER TABLE operations ADD CONSTRAINT operations_state_check
CHECK (state IN ('pending', 'processing', 'retry', 'succeeded', 'failed'));
