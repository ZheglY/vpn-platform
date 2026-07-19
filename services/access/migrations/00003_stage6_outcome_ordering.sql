-- +goose Up
CREATE TABLE access_assignment_snapshots (
    credential_id uuid NOT NULL REFERENCES access_credentials(id) ON DELETE CASCADE,
    allocation_revision integer NOT NULL CHECK (allocation_revision > 0),
    node_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (credential_id, allocation_revision, node_id)
);

UPDATE outbox
SET payload = jsonb_set(payload, '{aggregate_sequence}', payload #> '{data,desired_revision}', true)
WHERE topic IN ('access.provision.request.v1', 'access.revoke.request.v1');

INSERT INTO access_assignment_snapshots (credential_id, allocation_revision, node_id, created_at)
SELECT credential_id, allocation_revision, node_id, min(created_at)
FROM access_endpoint_snapshots
GROUP BY credential_id, allocation_revision, node_id
ON CONFLICT DO NOTHING;

CREATE TABLE provisioning_outcome_state (
    credential_id uuid PRIMARY KEY REFERENCES access_credentials(id) ON DELETE CASCADE,
    last_applied_sequence bigint NOT NULL DEFAULT 0 CHECK (last_applied_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

WITH ranked AS (
    SELECT event_id,
           row_number() OVER (PARTITION BY aggregate_id ORDER BY received_at, event_id) AS sequence
    FROM inbox
    WHERE event_type IN (
        'access.provision.succeeded.v1', 'access.provision.failed.v1',
        'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
    )
)
UPDATE inbox i
SET aggregate_sequence = ranked.sequence
FROM ranked
WHERE i.event_id = ranked.event_id AND i.aggregate_sequence IS NULL;

INSERT INTO provisioning_outcome_state (credential_id, last_applied_sequence)
SELECT aggregate_id, max(aggregate_sequence)
FROM inbox
WHERE event_type IN (
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
)
GROUP BY aggregate_id
ON CONFLICT (credential_id) DO UPDATE
SET last_applied_sequence = EXCLUDED.last_applied_sequence,
    updated_at = clock_timestamp();

DROP INDEX IF EXISTS inbox_lifecycle_sequence_idx;
CREATE UNIQUE INDEX inbox_lifecycle_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE event_type IN (
    'subscription.activated.v1', 'subscription.extended.v1',
    'subscription.expired.v1', 'subscription.revoked.v1'
);
CREATE UNIQUE INDEX inbox_provisioning_outcome_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE event_type IN (
    'access.provision.succeeded.v1', 'access.provision.failed.v1',
    'access.revoke.succeeded.v1', 'access.revoke.failed.v1'
);
ALTER TABLE inbox ADD CONSTRAINT inbox_ordered_event_sequence_required CHECK (
    aggregate_sequence IS NOT NULL
);

-- +goose Down
ALTER TABLE inbox DROP CONSTRAINT IF EXISTS inbox_ordered_event_sequence_required;
DROP INDEX IF EXISTS inbox_provisioning_outcome_sequence_idx;
DROP INDEX IF EXISTS inbox_lifecycle_sequence_idx;
CREATE UNIQUE INDEX inbox_lifecycle_sequence_idx
ON inbox (aggregate_id, aggregate_sequence)
WHERE aggregate_sequence IS NOT NULL;
DROP TABLE IF EXISTS provisioning_outcome_state;
DROP TABLE IF EXISTS access_assignment_snapshots;
