package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/catalog/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string, options ...platformpostgres.Option) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, dsn, options...)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) SeedPlan(ctx context.Context, plan domain.Plan, channel string) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	priceID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin catalog seed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
INSERT INTO plan_versions (plan_id, name, duration_days, grace_period_hours, region_policy, traffic_policy, primary_nodes, failover_nodes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (plan_id) DO NOTHING`, plan.PlanID, plan.Name, plan.DurationDays, plan.GracePeriodHours, plan.RegionPolicy, plan.TrafficPolicy, plan.PrimaryNodes, plan.FailoverNodes); err != nil {
		return fmt.Errorf("insert plan version: %w", err)
	}
	if _, err = tx.Exec(ctx, `
INSERT INTO plan_prices (price_id, plan_id, channel, amount_minor, currency)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (plan_id, channel) DO NOTHING`, priceID, plan.PlanID, channel, plan.AmountMinor, plan.Currency); err != nil {
		return fmt.Errorf("insert plan price: %w", err)
	}
	for _, region := range plan.Regions {
		if _, err = tx.Exec(ctx, `INSERT INTO plan_regions (plan_id, region) VALUES ($1,$2) ON CONFLICT DO NOTHING`, plan.PlanID, region); err != nil {
			return fmt.Errorf("insert plan region: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_publications (plan_id, channel) VALUES ($1,$2) ON CONFLICT DO NOTHING`, plan.PlanID, channel); err != nil {
		return fmt.Errorf("publish plan: %w", err)
	}
	stored, err := getPlan(ctx, tx, plan.PlanID, channel)
	if err != nil {
		return err
	}
	slices.Sort(stored.Regions)
	slices.Sort(plan.Regions)
	if !reflect.DeepEqual(stored, plan) {
		return fmt.Errorf("seed plan %q conflicts with immutable stored version", plan.PlanID)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit catalog seed: %w", err)
	}
	return nil
}

func (s *Store) ListPublished(ctx context.Context, channel string) ([]domain.Plan, error) {
	rows, err := s.pool.Query(ctx, `
SELECT plan_id FROM catalog_publications
WHERE channel = $1 AND published_at <= now() AND (retired_at IS NULL OR retired_at > now())
ORDER BY plan_id`, channel)
	if err != nil {
		return nil, fmt.Errorf("list published plans: %w", err)
	}
	defer rows.Close()
	var plans []domain.Plan
	for rows.Next() {
		var planID string
		if err := rows.Scan(&planID); err != nil {
			return nil, fmt.Errorf("scan published plan id: %w", err)
		}
		plan, err := getPlan(ctx, s.pool, planID, channel)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published plans: %w", err)
	}
	return plans, nil
}

func (s *Store) GetPublished(ctx context.Context, planID, channel string) (domain.Plan, error) {
	return getPlan(ctx, s.pool, planID, channel)
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getPlan(ctx context.Context, q querier, planID, channel string) (domain.Plan, error) {
	var plan domain.Plan
	err := q.QueryRow(ctx, `
SELECT v.plan_id, v.name, v.duration_days, v.grace_period_hours, pr.amount_minor, pr.currency,
       v.region_policy, v.traffic_policy, v.primary_nodes, v.failover_nodes,
       ARRAY(SELECT r.region FROM plan_regions r WHERE r.plan_id = v.plan_id ORDER BY r.region)
FROM plan_versions v
JOIN plan_prices pr ON pr.plan_id = v.plan_id AND pr.channel = $2
JOIN catalog_publications p ON p.plan_id = v.plan_id AND p.channel = $2
WHERE v.plan_id = $1 AND p.published_at <= now() AND (p.retired_at IS NULL OR p.retired_at > now())`, planID, channel).Scan(
		&plan.PlanID, &plan.Name, &plan.DurationDays, &plan.GracePeriodHours, &plan.AmountMinor, &plan.Currency,
		&plan.RegionPolicy, &plan.TrafficPolicy, &plan.PrimaryNodes, &plan.FailoverNodes, &plan.Regions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Plan{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Plan{}, fmt.Errorf("get published plan: %w", err)
	}
	return plan, nil
}
