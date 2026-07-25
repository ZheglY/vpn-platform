package retention

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunDryRunReportsWithoutDeleting(t *testing.T) {
	t.Parallel()
	deletes := 0
	report, err := Run(context.Background(), "billing_service", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC), Settings{
		DryRun: true, BatchSize: 10, MaxDelete: 20,
	}, Dataset{
		Name: "processed_webhooks", KeepFor: 30 * 24 * time.Hour,
		Preview: func(context.Context, time.Time) (Eligibility, error) {
			return Eligibility{Eligible: 12, Protected: 3}, nil
		},
		DeleteBatch: func(context.Context, time.Time, int) (int64, error) {
			deletes++
			return 0, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deletes != 0 || report.Datasets[0].Eligible != 12 || report.Datasets[0].Protected != 3 || report.Datasets[0].Deleted != 0 {
		t.Fatalf("unexpected dry-run report: %+v deletes=%d", report, deletes)
	}
}

func TestRunEnforcesPerRunAndBatchDeletionBounds(t *testing.T) {
	t.Parallel()
	calls := 0
	report, err := Run(context.Background(), "provisioning_service", time.Now().UTC(), Settings{
		BatchSize: 3, MaxDelete: 5,
	}, Dataset{
		Name: "node_health_snapshots", KeepFor: 30 * 24 * time.Hour,
		Preview: func(context.Context, time.Time) (Eligibility, error) {
			return Eligibility{Eligible: 9}, nil
		},
		DeleteBatch: func(_ context.Context, _ time.Time, limit int) (int64, error) {
			calls++
			return int64(limit), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := report.Datasets[0]
	if calls != 2 || item.Deleted != 5 || item.RemainingEstimate != 4 {
		t.Fatalf("unexpected bounded report: %+v calls=%d", item, calls)
	}
}

func TestRunRejectsStoreDeletionOverflow(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), "access_service", time.Now().UTC(), Settings{
		BatchSize: 2, MaxDelete: 2,
	}, Dataset{
		Name: "security_audit", KeepFor: 365 * 24 * time.Hour,
		Preview: func(context.Context, time.Time) (Eligibility, error) {
			return Eligibility{Eligible: 1}, nil
		},
		DeleteBatch: func(context.Context, time.Time, int) (int64, error) {
			return 3, nil
		},
	})
	if err == nil {
		t.Fatal("Run() accepted a deletion count larger than its batch")
	}
}

func TestRunStopsOnDatasetError(t *testing.T) {
	t.Parallel()
	want := errors.New("database unavailable")
	_, err := Run(context.Background(), "notification_service", time.Now().UTC(), Settings{
		DryRun: true, BatchSize: 10, MaxDelete: 10,
	}, Dataset{
		Name: "replayed_dead_letters", KeepFor: 30 * 24 * time.Hour,
		Preview: func(context.Context, time.Time) (Eligibility, error) {
			return Eligibility{}, want
		},
		DeleteBatch: func(context.Context, time.Time, int) (int64, error) { return 0, nil },
	})
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want wrapped store error", err)
	}
}
