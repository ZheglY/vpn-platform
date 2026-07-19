-- +goose Up
ALTER TABLE subscription_periods
    ADD COLUMN primary_nodes integer NOT NULL DEFAULT 1 CHECK (primary_nodes BETWEEN 1 AND 8),
    ADD COLUMN failover_nodes integer NOT NULL DEFAULT 1 CHECK (failover_nodes BETWEEN 0 AND 8),
    ADD CONSTRAINT subscription_periods_total_nodes CHECK (primary_nodes + failover_nodes BETWEEN 1 AND 8);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_subscription_period_immutability() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.subscription_id IS DISTINCT FROM OLD.subscription_id
       OR NEW.source_order_id IS DISTINCT FROM OLD.source_order_id
       OR NEW.source_payment_id IS DISTINCT FROM OLD.source_payment_id
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.region IS DISTINCT FROM OLD.region
       OR NEW.primary_nodes IS DISTINCT FROM OLD.primary_nodes
       OR NEW.failover_nodes IS DISTINCT FROM OLD.failover_nodes
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_subscription_period_immutability() RETURNS trigger
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

ALTER TABLE subscription_periods DROP CONSTRAINT IF EXISTS subscription_periods_total_nodes;
ALTER TABLE subscription_periods
    DROP COLUMN IF EXISTS failover_nodes,
    DROP COLUMN IF EXISTS primary_nodes;
