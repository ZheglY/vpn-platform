package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZheglY/vpn-platform/services/catalog/internal/domain"
)

type fakeStore struct {
	plans []domain.Plan
	err   error
}

func (f *fakeStore) Ping(context.Context) error                          { return f.err }
func (f *fakeStore) SeedPlan(context.Context, domain.Plan, string) error { return f.err }
func (f *fakeStore) ListPublished(context.Context, string) ([]domain.Plan, error) {
	return f.plans, f.err
}
func (f *fakeStore) GetPublished(_ context.Context, planID, _ string) (domain.Plan, error) {
	if f.err != nil {
		return domain.Plan{}, f.err
	}
	for _, plan := range f.plans {
		if plan.PlanID == planID {
			return plan, nil
		}
	}
	return domain.Plan{}, domain.ErrNotFound
}

func TestListPlans(t *testing.T) {
	plan := testPlan()
	req := httptest.NewRequest(http.MethodGet, "/v1/plans?channel=telegram", nil)
	rec := httptest.NewRecorder()
	New(&fakeStore{plans: []domain.Plan{plan}}).ListPlans(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var response struct {
		Plans []domain.Plan `json:"plans"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Plans) != 1 || response.Plans[0].PlanID != plan.PlanID {
		t.Fatalf("unexpected plans: %+v", response.Plans)
	}
}

func TestGetPlanRejectsUnsupportedRegion(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/plans/vpn-30d-v1?region=moon", nil)
	req.SetPathValue("plan_id", "vpn-30d-v1")
	rec := httptest.NewRecorder()
	New(&fakeStore{plans: []domain.Plan{testPlan()}}).GetPlan(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListPlansFailsClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/plans", nil)
	rec := httptest.NewRecorder()
	New(&fakeStore{err: errors.New("database unavailable")}).ListPlans(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func testPlan() domain.Plan {
	return domain.Plan{PlanID: "vpn-30d-v1", Name: "VPN 30 days", DurationDays: 30, GracePeriodHours: 24, AmountMinor: 29900, Currency: "RUB", RegionPolicy: "single_region_with_failover", TrafficPolicy: "no_hard_cap", PrimaryNodes: 1, FailoverNodes: 1, Regions: []string{"ru-test"}}
}
