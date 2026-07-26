package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
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

func TestDumpArgumentsBindExportedSnapshot(t *testing.T) {
	t.Parallel()
	arguments := dumpArguments("00000003-0000001B-1")
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "--snapshot=00000003-0000001B-1") {
		t.Fatalf("dump arguments do not bind exported snapshot: %v", arguments)
	}
	if strings.Contains(joined, "clean") || strings.Contains(joined, "password") {
		t.Fatalf("unsafe dump arguments: %v", arguments)
	}
}

func TestBackupDoesNotRemovePreexistingOutput(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "existing.dump.age")
	if err := os.WriteFile(output, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "postgres://backup_user:password@postgres:5432/billing_service?sslmode=disable")
	err := backup([]string{
		"--database", "billing_service",
		"--owner", "billing_app",
		"--recipient", filepath.Join(directory, "missing.recipient"),
		"--output", output,
		"--metadata", filepath.Join(directory, "metadata.json"),
		"--inspection", filepath.Join(directory, "inspection.json"),
	})
	if err == nil {
		t.Fatal("backup accepted a preexisting output")
	}
	body, readErr := os.ReadFile(output)
	if readErr != nil || string(body) != "preserve" {
		t.Fatalf("preexisting output was changed: body=%q err=%v", body, readErr)
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

type spyRestoreRunner struct {
	called bool
}

func (s *spyRestoreRunner) Run(context.Context, []string, string, string, io.Reader) error {
	s.called = true
	return nil
}

func TestIntegrationRestoreRejectsNonEmptyTargetBeforePgRestore(t *testing.T) {
	databaseURL := os.Getenv("BACKUPCTL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BACKUPCTL_TEST_DATABASE_URL is not set")
	}
	_, database, _, err := parseDatabaseURL(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext := context.Background()
		if _, cleanupErr := connection.Exec(cleanupContext, `DROP TABLE IF EXISTS backupctl_restore_sentinel`); cleanupErr != nil {
			t.Errorf("drop restore sentinel: %v", cleanupErr)
		}
		if closeErr := connection.Close(cleanupContext); closeErr != nil {
			t.Errorf("close restore test connection: %v", closeErr)
		}
	})
	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS backupctl_restore_sentinel (id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(directory, "restore.agekey")
	inputPath := filepath.Join(directory, "backup.dump.age")
	metadataPath := filepath.Join(directory, "backup.metadata.json")
	if err := os.WriteFile(identityPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("not reached by pg_restore")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, encrypted.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := fileDigest(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata := artifactMetadata{
		FormatVersion: artifactVersion, Database: database, ExpectedOwner: "restore_owner",
		CreatedAt: time.Now().UTC(), Tool: "pg_dump-custom+age-x25519",
		CiphertextSHA256: digest, CiphertextBytes: size,
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", databaseURL)
	runner := &spyRestoreRunner{}
	err = restoreWithRunner([]string{
		"--identity", identityPath,
		"--input", inputPath,
		"--metadata", metadataPath,
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("restore error = %v, want nonempty target rejection", err)
	}
	if runner.called {
		t.Fatal("pg_restore runner was invoked for a nonempty target")
	}
}
