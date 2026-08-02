package retention

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var datasetNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)

type Eligibility struct {
	Eligible  int64
	Protected int64
}

type Dataset struct {
	Name        string
	KeepFor     time.Duration
	Preview     func(context.Context, time.Time) (Eligibility, error)
	DeleteBatch func(context.Context, time.Time, int) (int64, error)
}

type DatasetReport struct {
	Dataset           string    `json:"dataset"`
	Cutoff            time.Time `json:"cutoff"`
	Eligible          int64     `json:"eligible"`
	Protected         int64     `json:"protected"`
	Deleted           int64     `json:"deleted"`
	RemainingEstimate int64     `json:"remaining_estimate"`
}

type Report struct {
	Owner       string          `json:"owner"`
	DryRun      bool            `json:"dry_run"`
	Status      string          `json:"status"`
	GeneratedAt time.Time       `json:"generated_at"`
	BatchSize   int             `json:"batch_size"`
	MaxDelete   int             `json:"max_delete"`
	Datasets    []DatasetReport `json:"datasets"`
	Failure     *FailureReport  `json:"failure,omitempty"`
}

type FailureReport struct {
	Dataset string `json:"dataset"`
	Stage   string `json:"stage"`
}

type Settings struct {
	DryRun    bool
	BatchSize int
	MaxDelete int
}

func Run(ctx context.Context, owner string, now time.Time, settings Settings, datasets ...Dataset) (Report, error) {
	if !datasetNamePattern.MatchString(owner) {
		return Report{}, fmt.Errorf("retention owner is invalid")
	}
	if now.IsZero() {
		return Report{}, fmt.Errorf("retention time is required")
	}
	if settings.BatchSize < 1 || settings.BatchSize > 1000 {
		return Report{}, fmt.Errorf("retention batch size must be between 1 and 1000")
	}
	if settings.MaxDelete < settings.BatchSize || settings.MaxDelete > 100000 {
		return Report{}, fmt.Errorf("retention max delete must be between batch size and 100000")
	}
	report := Report{
		Owner: owner, DryRun: settings.DryRun, Status: "completed", GeneratedAt: now.UTC(),
		BatchSize: settings.BatchSize, MaxDelete: settings.MaxDelete,
		Datasets: make([]DatasetReport, 0, len(datasets)),
	}
	seen := make(map[string]struct{}, len(datasets))
	for _, dataset := range datasets {
		if !datasetNamePattern.MatchString(dataset.Name) || dataset.KeepFor <= 0 || dataset.Preview == nil || dataset.DeleteBatch == nil {
			return Report{}, fmt.Errorf("retention dataset is invalid")
		}
		if _, exists := seen[dataset.Name]; exists {
			return Report{}, fmt.Errorf("retention dataset is duplicated")
		}
		seen[dataset.Name] = struct{}{}
	}
	for _, dataset := range datasets {
		cutoff := now.UTC().Add(-dataset.KeepFor)
		eligibility, err := dataset.Preview(ctx, cutoff)
		if err != nil {
			report.Status = "failed"
			report.Failure = &FailureReport{Dataset: dataset.Name, Stage: "preview"}
			return report, fmt.Errorf("preview retention dataset %s: %w", dataset.Name, err)
		}
		if eligibility.Eligible < 0 || eligibility.Protected < 0 {
			report.Status = "failed"
			report.Failure = &FailureReport{Dataset: dataset.Name, Stage: "preview_result"}
			return report, fmt.Errorf("retention dataset returned invalid counts")
		}
		item := DatasetReport{
			Dataset: dataset.Name, Cutoff: cutoff,
			Eligible: eligibility.Eligible, Protected: eligibility.Protected,
			RemainingEstimate: eligibility.Eligible,
		}
		if !settings.DryRun {
			for item.Deleted < int64(settings.MaxDelete) {
				limit := min(settings.BatchSize, settings.MaxDelete-int(item.Deleted))
				deleted, err := dataset.DeleteBatch(ctx, cutoff, limit)
				if err != nil {
					item.RemainingEstimate = max(eligibility.Eligible-item.Deleted, 0)
					report.Datasets = append(report.Datasets, item)
					report.Status = "failed"
					report.Failure = &FailureReport{Dataset: dataset.Name, Stage: "delete"}
					return report, fmt.Errorf("delete retention dataset %s: %w", dataset.Name, err)
				}
				if deleted < 0 || deleted > int64(limit) {
					item.RemainingEstimate = max(eligibility.Eligible-item.Deleted, 0)
					report.Datasets = append(report.Datasets, item)
					report.Status = "failed"
					report.Failure = &FailureReport{Dataset: dataset.Name, Stage: "delete_result"}
					return report, fmt.Errorf("retention dataset exceeded its deletion bound")
				}
				item.Deleted += deleted
				if deleted < int64(limit) {
					break
				}
			}
			item.RemainingEstimate = max(eligibility.Eligible-item.Deleted, 0)
		}
		report.Datasets = append(report.Datasets, item)
	}
	return report, nil
}
