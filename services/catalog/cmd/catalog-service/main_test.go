package main

import "testing"

func TestLoadConfigDoesNotRequireSeedValuesForService(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://catalog.invalid/catalog")
	t.Setenv("INTERNAL_AUTH_MODE", "dev-insecure")
	t.Setenv("CATALOG_SEED_AMOUNT_MINOR", "not-an-integer")

	if _, err := loadConfig(false); err != nil {
		t.Fatalf("service config depends on seed values: %v", err)
	}
	if _, err := loadConfig(true); err == nil {
		t.Fatal("seed config accepted an invalid amount")
	}
}
