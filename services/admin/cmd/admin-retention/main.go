package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
	adminpostgres "github.com/ZheglY/vpn-platform/services/admin/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "admin retention job failed")
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
	keepFor, err := retention.DaysFromEnv("ADMIN_AUDIT_RETENTION_DAYS", 365, 365, 3650)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := adminpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	now, err := store.RetentionClock(ctx)
	if err != nil {
		return err
	}
	report, runErr := retention.Run(ctx, "admin_service", now, settings, store.RetentionDatasets(keepFor)...)
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(report))
}
