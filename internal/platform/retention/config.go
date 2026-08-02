package retention

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func SettingsFromEnv() (Settings, error) {
	dryRun, err := envBool("RETENTION_DRY_RUN", true)
	if err != nil {
		return Settings{}, err
	}
	batchSize, err := envInt("RETENTION_BATCH_SIZE", 500, 1, 1000)
	if err != nil {
		return Settings{}, err
	}
	maxDelete, err := envInt("RETENTION_MAX_DELETE", 5000, batchSize, 100000)
	if err != nil {
		return Settings{}, err
	}
	return Settings{DryRun: dryRun, BatchSize: batchSize, MaxDelete: maxDelete}, nil
}

func DaysFromEnv(name string, fallback, minimum, maximum int) (time.Duration, error) {
	days, err := envInt(name, fallback, minimum, maximum)
	if err != nil {
		return 0, err
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return parsed, nil
}

func envInt(name string, fallback, minimum, maximum int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}
