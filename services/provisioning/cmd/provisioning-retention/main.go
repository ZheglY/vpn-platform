package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/retention"
	provisioningpostgres "github.com/ZheglY/vpn-platform/services/provisioning/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "provisioning retention job failed")
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
	keepFor, err := retention.DaysFromEnv("PROVISIONING_DIAGNOSTIC_RETENTION_DAYS", 30, 1, 3650)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := provisioningpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	report, err := retention.Run(ctx, "provisioning_service", time.Now().UTC(), settings, store.RetentionDatasets(keepFor)...)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
