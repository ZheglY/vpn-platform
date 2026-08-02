package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
	billingpostgres "github.com/ZheglY/vpn-platform/services/billing/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "billing retention job failed")
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	settings, err := retention.SettingsFromEnv()
	if err != nil {
		return err
	}
	keepFor, err := retention.DaysFromEnv("BILLING_WEBHOOK_RETENTION_DAYS", 30, 1, 3650)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := billingpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	now, err := store.RetentionClock(ctx)
	if err != nil {
		return err
	}
	report, runErr := retention.Run(ctx, "billing_service", now, settings, store.RetentionDatasets(keepFor)...)
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(report))
}
