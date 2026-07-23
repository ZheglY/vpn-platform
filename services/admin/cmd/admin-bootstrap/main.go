package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	adminpostgres "github.com/ZheglY/vpn-platform/services/admin/internal/postgres"
)

var principalPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	seedFile := strings.TrimSpace(os.Getenv("ADMIN_SEED_FILE"))
	trustDomain := strings.TrimSpace(os.Getenv("MTLS_TRUST_DOMAIN"))
	environment := strings.TrimSpace(os.Getenv("APP_ENV"))
	if databaseURL == "" || seedFile == "" || trustDomain == "" || environment == "" {
		return fmt.Errorf("DATABASE_URL, ADMIN_SEED_FILE, MTLS_TRUST_DOMAIN, and APP_ENV are required")
	}
	file, err := os.Open(seedFile)
	if err != nil {
		return fmt.Errorf("open admin seed: %w", err)
	}
	defer func() { _ = file.Close() }()
	var document struct {
		Principals []adminpostgres.PrincipalSeed `json:"principals"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode admin seed: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("admin seed contains multiple JSON values")
	}
	if len(document.Principals) < 1 || len(document.Principals) > 100 {
		return fmt.Errorf("admin seed must contain 1 to 100 principals")
	}
	seen := make(map[string]struct{})
	validRoles := []string{"support_readonly", "operations", "security", "finance_readonly"}
	for _, principal := range document.Principals {
		parsed, err := url.Parse(principal.SPIFFEID)
		parts := []string(nil)
		if err == nil {
			parts = strings.Split(strings.Trim(parsed.Path, "/"), "/")
		}
		if err != nil || parsed.Scheme != "spiffe" || parsed.Host != trustDomain || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || len(parts) != 4 || parts[0] != "ns" || parts[1] != environment || parts[2] != "admin" || !principalPattern.MatchString(parts[3]) {
			return fmt.Errorf("admin seed contains invalid SPIFFE identity")
		}
		if _, exists := seen[principal.SPIFFEID]; exists {
			return fmt.Errorf("admin seed contains duplicate SPIFFE identity")
		}
		seen[principal.SPIFFEID] = struct{}{}
		if len(strings.TrimSpace(principal.DisplayName)) < 1 || len(principal.DisplayName) > 128 || len(principal.Roles) < 1 || len(principal.Roles) > 4 {
			return fmt.Errorf("admin seed principal metadata is invalid")
		}
		roleSeen := make(map[string]struct{})
		for _, role := range principal.Roles {
			if !slices.Contains(validRoles, role) {
				return fmt.Errorf("admin seed contains unknown role")
			}
			if _, exists := roleSeen[role]; exists {
				return fmt.Errorf("admin seed contains duplicate role")
			}
			roleSeen[role] = struct{}{}
		}
	}
	store, err := adminpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Bootstrap(ctx, document.Principals)
}
