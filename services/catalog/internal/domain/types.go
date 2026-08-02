package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrNotFound = errors.New("catalog plan not found")

type Plan struct {
	PlanID           string   `json:"plan_id"`
	Name             string   `json:"name"`
	DurationDays     int      `json:"duration_days"`
	GracePeriodHours int      `json:"grace_period_hours"`
	AmountMinor      int64    `json:"amount_minor"`
	Currency         string   `json:"currency"`
	RegionPolicy     string   `json:"region_policy"`
	TrafficPolicy    string   `json:"traffic_policy"`
	PrimaryNodes     int      `json:"primary_nodes"`
	FailoverNodes    int      `json:"failover_nodes"`
	Regions          []string `json:"regions"`
}

func (p Plan) Validate() error {
	if strings.TrimSpace(p.PlanID) == "" || len(p.PlanID) > 128 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
		return fmt.Errorf("plan id and name are required")
	}
	if p.DurationDays <= 0 || p.DurationDays > 366 || p.GracePeriodHours < 0 || p.GracePeriodHours > 168 {
		return fmt.Errorf("invalid plan duration or grace period")
	}
	if p.AmountMinor <= 0 || len(p.Currency) != 3 || p.Currency != strings.ToUpper(p.Currency) {
		return fmt.Errorf("invalid plan price")
	}
	if p.RegionPolicy != "single_region_with_failover" || p.TrafficPolicy != "no_hard_cap" {
		return fmt.Errorf("unsupported plan policy")
	}
	if p.PrimaryNodes != 1 || p.FailoverNodes != 1 {
		return fmt.Errorf("MVP plan requires one primary and one failover node")
	}
	if len(p.Regions) == 0 {
		return fmt.Errorf("at least one region is required")
	}
	seen := make(map[string]struct{}, len(p.Regions))
	for _, region := range p.Regions {
		if strings.TrimSpace(region) != region || len(region) < 2 || len(region) > 64 {
			return fmt.Errorf("invalid region")
		}
		if _, exists := seen[region]; exists {
			return fmt.Errorf("duplicate region")
		}
		seen[region] = struct{}{}
	}
	return nil
}

func (p Plan) SupportsRegion(region string) bool { return slices.Contains(p.Regions, region) }

type Store interface {
	Ping(context.Context) error
	SeedPlan(context.Context, Plan, string) error
	ListPublished(context.Context, string) ([]Plan, error)
	GetPublished(context.Context, string, string) (Plan, error)
}
