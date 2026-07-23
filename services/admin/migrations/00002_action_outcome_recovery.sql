-- +goose Up
ALTER TABLE admin_action_requests
    DROP CONSTRAINT admin_action_requests_status_check,
    DROP CONSTRAINT admin_action_requests_check;

ALTER TABLE admin_action_requests
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN claim_id uuid NULL,
    ADD COLUMN lease_until timestamptz NULL,
    ADD COLUMN last_attempt_at timestamptz NULL;

UPDATE admin_action_requests
SET attempts = 1,
    last_attempt_at = COALESCE(completed_at, created_at)
WHERE status IN ('succeeded', 'failed');

ALTER TABLE admin_action_requests
    ADD CONSTRAINT admin_action_requests_status_check
        CHECK (status IN ('pending', 'processing', 'outcome_unknown', 'succeeded', 'failed')),
    ADD CONSTRAINT admin_action_requests_state_check CHECK (
        (status = 'pending' AND completed_at IS NULL AND result IS NULL AND error_code IS NULL
            AND claim_id IS NULL AND lease_until IS NULL)
        OR (status = 'processing' AND completed_at IS NULL AND result IS NULL AND error_code IS NULL
            AND claim_id IS NOT NULL AND lease_until IS NOT NULL AND attempts > 0 AND last_attempt_at IS NOT NULL)
        OR (status = 'outcome_unknown' AND completed_at IS NULL AND result IS NULL AND error_code IS NOT NULL
            AND claim_id IS NULL AND lease_until IS NULL AND attempts > 0 AND last_attempt_at IS NOT NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL AND result IS NOT NULL AND error_code IS NULL
            AND claim_id IS NULL AND lease_until IS NULL AND attempts > 0 AND last_attempt_at IS NOT NULL)
        OR (status = 'failed' AND completed_at IS NOT NULL AND result IS NULL AND error_code IS NOT NULL
            AND claim_id IS NULL AND lease_until IS NULL AND attempts > 0 AND last_attempt_at IS NOT NULL)
    );

ALTER TABLE admin_audit_events
    DROP CONSTRAINT admin_audit_events_outcome_check,
    DROP CONSTRAINT admin_audit_events_check;

ALTER TABLE admin_audit_events
    ADD CONSTRAINT admin_audit_events_outcome_check
        CHECK (outcome IN ('accepted', 'attempted', 'retrying', 'outcome_unknown', 'succeeded', 'failed')),
    ADD CONSTRAINT admin_audit_events_state_check CHECK (
        (outcome IN ('accepted', 'attempted', 'retrying') AND completed_at IS NULL AND error_code IS NULL)
        OR (outcome = 'outcome_unknown' AND completed_at IS NULL AND error_code IS NOT NULL)
        OR (outcome = 'succeeded' AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (outcome = 'failed' AND completed_at IS NOT NULL AND error_code IS NOT NULL)
    );

CREATE INDEX admin_actions_recovery_idx
    ON admin_action_requests (lease_until, created_at)
    WHERE status IN ('processing', 'outcome_unknown');

-- +goose Down
DROP INDEX IF EXISTS admin_actions_recovery_idx;

DROP TRIGGER IF EXISTS admin_audit_no_delete ON admin_audit_events;
DROP TRIGGER IF EXISTS admin_audit_no_update ON admin_audit_events;

DELETE FROM admin_audit_events
WHERE outcome IN ('attempted', 'retrying', 'outcome_unknown')
   OR action_id IN (
       SELECT action_id FROM admin_action_requests
       WHERE status IN ('processing', 'outcome_unknown')
   );

ALTER TABLE admin_audit_events
    DROP CONSTRAINT admin_audit_events_state_check,
    DROP CONSTRAINT admin_audit_events_outcome_check,
    ADD CONSTRAINT admin_audit_events_outcome_check
        CHECK (outcome IN ('accepted', 'succeeded', 'failed')),
    ADD CONSTRAINT admin_audit_events_check CHECK (
        (outcome = 'accepted' AND completed_at IS NULL AND error_code IS NULL)
        OR (outcome = 'succeeded' AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (outcome = 'failed' AND completed_at IS NOT NULL AND error_code IS NOT NULL)
    );

DELETE FROM admin_action_requests WHERE status IN ('processing', 'outcome_unknown');

ALTER TABLE admin_action_requests
    DROP CONSTRAINT admin_action_requests_state_check,
    DROP CONSTRAINT admin_action_requests_status_check,
    DROP COLUMN last_attempt_at,
    DROP COLUMN lease_until,
    DROP COLUMN claim_id,
    DROP COLUMN attempts,
    ADD CONSTRAINT admin_action_requests_status_check
        CHECK (status IN ('pending', 'succeeded', 'failed')),
    ADD CONSTRAINT admin_action_requests_check CHECK (
        (status = 'pending' AND completed_at IS NULL AND result IS NULL AND error_code IS NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL AND result IS NOT NULL AND error_code IS NULL)
        OR (status = 'failed' AND completed_at IS NOT NULL AND result IS NULL AND error_code IS NOT NULL)
    );

CREATE TRIGGER admin_audit_no_update
BEFORE UPDATE ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION reject_admin_audit_mutation();
CREATE TRIGGER admin_audit_no_delete
BEFORE DELETE ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION reject_admin_audit_mutation();
