package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
	notificationpostgres "github.com/ZheglY/vpn-platform/services/notification/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "notification retention job failed")
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
	keepFor, err := retention.DaysFromEnv("NOTIFICATION_DLQ_RETENTION_DAYS", 30, 1, 3650)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := notificationpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	now, err := store.RetentionClock(ctx)
	if err != nil {
		return err
	}
	report, runErr := retention.Run(ctx, "notification_service", now, settings, store.RetentionDatasets(keepFor)...)
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(report))
}
