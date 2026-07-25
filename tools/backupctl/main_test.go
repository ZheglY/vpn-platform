package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeygenCreatesRestrictedIdentityAndParseableRecipient(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	identity := filepath.Join(directory, "backup.agekey")
	recipient := filepath.Join(directory, "backup.recipient")
	if err := keygen([]string{"--identity", identity, "--recipient", recipient}); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(identity); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecipient(recipient); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(identity); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("identity permissions = %v", info.Mode().Perm())
	}
	if err := keygen([]string{"--identity", identity, "--recipient", recipient}); err == nil {
		t.Fatal("keygen overwrote an existing identity")
	}
}

func TestCompareInspectionsRejectsRowsOrOwners(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.json")
	targetPath := filepath.Join(directory, "target.json")
	source := databaseInspection{
		FormatVersion: artifactVersion, Database: "billing_service", ExpectedOwner: "billing_app",
		TableRows: map[string]int64{"payments": 2},
	}
	target := source
	target.TableRows = map[string]int64{"payments": 1}
	if err := writeJSONExclusive(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONExclusive(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compareInspections([]string{"--source", sourcePath, "--target", targetPath}); err == nil {
		t.Fatal("compare accepted mismatched row counts")
	}
}

func TestSafeIdentifier(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"identity_service", "admin_migrator", "goose_db_version"} {
		if !safeIdentifier(valid) {
			t.Fatalf("safeIdentifier(%q) = false", valid)
		}
	}
	for _, invalid := range []string{"db-name", "db;drop", "UPPER", "a"} {
		if safeIdentifier(invalid) {
			t.Fatalf("safeIdentifier(%q) = true", invalid)
		}
	}
}

func TestDatabaseEnvironmentKeepsPasswordOutOfArgumentsAndSplitsURI(t *testing.T) {
	t.Parallel()
	environment, err := databaseEnvironment("postgres://backup_user:p%40ss@postgres:5432/billing_service?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	serialized := strings.Join(environment, "\n")
	for _, expected := range []string{
		"PGHOST=postgres", "PGPORT=5432", "PGUSER=backup_user",
		"PGPASSWORD=p@ss", "PGDATABASE=billing_service", "PGSSLMODE=require",
	} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("database environment is missing %q", expected)
		}
	}
	if strings.Contains(serialized, "DATABASE_URL=") {
		t.Fatal("database environment retained DATABASE_URL")
	}
}

func TestRequireDatabaseRejectsMetadataMismatch(t *testing.T) {
	t.Parallel()
	databaseURL := "postgres://backup_user:password@postgres:5432/billing_service?sslmode=require"
	if err := requireDatabase(databaseURL, "billing_service"); err != nil {
		t.Fatalf("requireDatabase() rejected matching database: %v", err)
	}
	if err := requireDatabase(databaseURL, "access_service"); err == nil {
		t.Fatal("requireDatabase() accepted mismatched database metadata")
	}
}
