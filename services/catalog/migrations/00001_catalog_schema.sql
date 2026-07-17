-- +goose Up
CREATE TABLE plan_versions (
    plan_id text PRIMARY KEY CHECK (length(plan_id) BETWEEN 1 AND 128),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    duration_days integer NOT NULL CHECK (duration_days BETWEEN 1 AND 366),
    grace_period_hours integer NOT NULL CHECK (grace_period_hours BETWEEN 0 AND 168),
    region_policy text NOT NULL CHECK (region_policy = 'single_region_with_failover'),
    traffic_policy text NOT NULL CHECK (traffic_policy = 'no_hard_cap'),
    primary_nodes integer NOT NULL CHECK (primary_nodes = 1),
    failover_nodes integer NOT NULL CHECK (failover_nodes = 1),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan_prices (
    price_id uuid PRIMARY KEY,
    plan_id text NOT NULL REFERENCES plan_versions(plan_id) ON DELETE RESTRICT,
    channel text NOT NULL CHECK (channel = 'telegram'),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, channel)
);

CREATE TABLE plan_regions (
    plan_id text NOT NULL REFERENCES plan_versions(plan_id) ON DELETE RESTRICT,
    region text NOT NULL CHECK (length(region) BETWEEN 2 AND 64),
    PRIMARY KEY (plan_id, region)
);

CREATE TABLE catalog_publications (
    plan_id text NOT NULL REFERENCES plan_versions(plan_id) ON DELETE RESTRICT,
    channel text NOT NULL CHECK (channel = 'telegram'),
    published_at timestamptz NOT NULL DEFAULT now(),
    retired_at timestamptz NULL CHECK (retired_at IS NULL OR retired_at >= published_at),
    PRIMARY KEY (plan_id, channel)
);

-- +goose StatementBegin
CREATE FUNCTION reject_immutable_catalog_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published plan versions and prices are immutable';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER plan_versions_immutable
BEFORE UPDATE OR DELETE ON plan_versions
FOR EACH ROW EXECUTE FUNCTION reject_immutable_catalog_change();

CREATE TRIGGER plan_prices_immutable
BEFORE UPDATE OR DELETE ON plan_prices
FOR EACH ROW EXECUTE FUNCTION reject_immutable_catalog_change();

-- +goose StatementBegin
CREATE FUNCTION reject_published_region_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    affected_plan_id text;
BEGIN
    affected_plan_id := COALESCE(NEW.plan_id, OLD.plan_id);
    IF EXISTS (SELECT 1 FROM catalog_publications WHERE plan_id = affected_plan_id) THEN
        RAISE EXCEPTION 'published plan regions are immutable';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER plan_regions_immutable_after_publication
BEFORE INSERT OR UPDATE OR DELETE ON plan_regions
FOR EACH ROW EXECUTE FUNCTION reject_published_region_change();

-- +goose Down
DROP TRIGGER IF EXISTS plan_regions_immutable_after_publication ON plan_regions;
DROP FUNCTION IF EXISTS reject_published_region_change();
DROP TRIGGER IF EXISTS plan_prices_immutable ON plan_prices;
DROP TRIGGER IF EXISTS plan_versions_immutable ON plan_versions;
DROP FUNCTION IF EXISTS reject_immutable_catalog_change();
DROP TABLE IF EXISTS catalog_publications;
DROP TABLE IF EXISTS plan_regions;
DROP TABLE IF EXISTS plan_prices;
DROP TABLE IF EXISTS plan_versions;
