package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
)

const artifactVersion = 1

type artifactMetadata struct {
	FormatVersion    int       `json:"format_version"`
	Database         string    `json:"database"`
	ExpectedOwner    string    `json:"expected_owner"`
	CreatedAt        time.Time `json:"created_at"`
	Tool             string    `json:"tool"`
	CiphertextSHA256 string    `json:"ciphertext_sha256"`
	CiphertextBytes  int64     `json:"ciphertext_bytes"`
	DurationMillis   int64     `json:"duration_millis"`
}

type databaseInspection struct {
	FormatVersion   int              `json:"format_version"`
	Database        string           `json:"database"`
	ExpectedOwner   string           `json:"expected_owner"`
	InspectedAt     time.Time        `json:"inspected_at"`
	TableRows       map[string]int64 `json:"table_rows"`
	OwnerViolations []string         `json:"owner_violations"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "backup operation failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("backup command is required")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:])
	case "backup":
		return backup(args[1:])
	case "restore":
		return restore(args[1:])
	case "inspect":
		return inspectDatabase(args[1:])
	case "compare":
		return compareInspections(args[1:])
	default:
		return fmt.Errorf("unknown backup command")
	}
}

func keygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	identityPath := flags.String("identity", "", "")
	recipientPath := flags.String("recipient", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *identityPath == "" || *recipientPath == "" {
		return fmt.Errorf("invalid keygen arguments")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return fmt.Errorf("generate backup identity")
	}
	if err := writeExclusive(*identityPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		return err
	}
	if err := writeExclusive(*recipientPath, []byte(identity.Recipient().String()+"\n"), 0o644); err != nil {
		_ = os.Remove(*identityPath)
		return err
	}
	return nil
}

func backup(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("database", "", "")
	owner := flags.String("owner", "", "")
	recipientPath := flags.String("recipient", "", "")
	outputPath := flags.String("output", "", "")
	metadataPath := flags.String("metadata", "", "")
	inspectionPath := flags.String("inspection", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !safeIdentifier(*database) || !safeIdentifier(*owner) || *recipientPath == "" || *outputPath == "" || *metadataPath == "" || *inspectionPath == "" {
		return fmt.Errorf("invalid backup arguments")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("database URL is required")
	}
	if err := requireDatabase(databaseURL, *database); err != nil {
		return err
	}
	for _, path := range []string{*outputPath, *metadataPath, *inspectionPath} {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup output already exists or cannot be inspected")
		}
	}
	commandContext, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(commandContext, databaseURL)
	if err != nil {
		return fmt.Errorf("connect for consistent backup")
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()
	transaction, err := connection.BeginTx(commandContext, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin consistent backup")
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			_ = transaction.Rollback(context.Background())
		}
	}()
	if _, err := transaction.Exec(commandContext, `SET LOCAL statement_timeout = '30s'`); err != nil {
		return fmt.Errorf("bound consistent backup inspection")
	}
	var snapshotID string
	if err := transaction.QueryRow(commandContext, `SELECT pg_export_snapshot()`).Scan(&snapshotID); err != nil || snapshotID == "" {
		return fmt.Errorf("export consistent backup snapshot")
	}
	inspection, err := inspectSnapshot(commandContext, transaction, *database, *owner)
	if err != nil {
		return err
	}
	recipient, err := readRecipient(*recipientPath)
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	output, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create encrypted backup")
	}
	complete := false
	metadataCreated := false
	inspectionCreated := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(*outputPath)
			if metadataCreated {
				_ = os.Remove(*metadataPath)
			}
			if inspectionCreated {
				_ = os.Remove(*inspectionPath)
			}
		}
	}()
	encrypted, err := age.Encrypt(output, recipient)
	if err != nil {
		return fmt.Errorf("initialize backup encryption")
	}
	command := exec.CommandContext(commandContext, "pg_dump", dumpArguments(snapshotID)...)
	commandEnvironment, err := databaseEnvironment(databaseURL)
	if err != nil {
		return err
	}
	command.Env = commandEnvironment
	command.Stdout = encrypted
	var commandError bytes.Buffer
	command.Stderr = &boundedWriter{buffer: &commandError, remaining: 4096}
	if err := command.Run(); err != nil {
		return fmt.Errorf("database dump failed: %s", postgresFailureCode(commandError.String()))
	}
	if err := transaction.Rollback(commandContext); err != nil {
		return fmt.Errorf("release consistent backup snapshot")
	}
	transactionOpen = false
	if err := encrypted.Close(); err != nil {
		return fmt.Errorf("finalize backup encryption")
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync encrypted backup")
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close encrypted backup")
	}
	digest, size, err := fileDigest(*outputPath)
	if err != nil {
		return err
	}
	metadata := artifactMetadata{
		FormatVersion: artifactVersion, Database: *database, ExpectedOwner: *owner,
		CreatedAt: inspection.InspectedAt, Tool: "pg_dump-custom+age-x25519",
		CiphertextSHA256: digest, CiphertextBytes: size,
		DurationMillis: time.Since(startedAt).Milliseconds(),
	}
	if err := writeJSONExclusive(*metadataPath, metadata, 0o600); err != nil {
		return err
	}
	metadataCreated = true
	if err := writeJSONExclusive(*inspectionPath, inspection, 0o600); err != nil {
		return err
	}
	inspectionCreated = true
	complete = true
	return json.NewEncoder(os.Stdout).Encode(metadata)
}

func dumpArguments(snapshotID string) []string {
	return []string{"--format=custom", "--no-owner", "--no-acl", "--snapshot=" + snapshotID}
}

type restoreRunner interface {
	Run(context.Context, []string, string, string, io.Reader) error
}

type postgresRestoreRunner struct{}

func (postgresRestoreRunner) Run(ctx context.Context, environment []string, database, owner string, input io.Reader) error {
	command := exec.CommandContext(ctx, "pg_restore",
		"--exit-on-error", "--single-transaction",
		"--no-owner", "--no-acl", "--role="+owner,
		"--dbname="+database)
	command.Env = environment
	command.Stdin = input
	command.Stdout = io.Discard
	var commandError bytes.Buffer
	command.Stderr = &boundedWriter{buffer: &commandError, remaining: 4096}
	if err := command.Run(); err != nil {
		return fmt.Errorf("database restore failed: %s", postgresFailureCode(commandError.String()))
	}
	return nil
}

func restore(args []string) error {
	return restoreWithRunner(args, postgresRestoreRunner{})
}

func restoreWithRunner(args []string, runner restoreRunner) (resultErr error) {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	identityPath := flags.String("identity", "", "")
	inputPath := flags.String("input", "", "")
	metadataPath := flags.String("metadata", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *identityPath == "" || *inputPath == "" || *metadataPath == "" {
		return fmt.Errorf("invalid restore arguments")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("database URL is required")
	}
	if runner == nil {
		return fmt.Errorf("restore runner is required")
	}
	var metadata artifactMetadata
	if err := readJSON(*metadataPath, &metadata); err != nil || metadata.FormatVersion != artifactVersion || !safeIdentifier(metadata.Database) || !safeIdentifier(metadata.ExpectedOwner) {
		return fmt.Errorf("invalid backup metadata")
	}
	if err := requireDatabase(databaseURL, metadata.Database); err != nil {
		return err
	}
	digest, size, err := fileDigest(*inputPath)
	if err != nil {
		return err
	}
	if digest != metadata.CiphertextSHA256 || size != metadata.CiphertextBytes {
		return fmt.Errorf("encrypted backup integrity mismatch")
	}
	identity, err := readIdentity(*identityPath)
	if err != nil {
		return err
	}
	if err := requireEmptyRestoreTarget(context.Background(), databaseURL, metadata.Database); err != nil {
		return err
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open encrypted backup")
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeFile(input, "close encrypted backup"))
	}()
	decrypted, err := age.Decrypt(input, identity)
	if err != nil {
		return fmt.Errorf("decrypt backup")
	}
	commandContext, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	commandEnvironment, err := databaseEnvironment(databaseURL)
	if err != nil {
		return err
	}
	return runner.Run(commandContext, commandEnvironment, metadata.Database, metadata.ExpectedOwner, decrypted)
}

func requireEmptyRestoreTarget(parent context.Context, databaseURL, expectedDatabase string) (resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to restore target")
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeConnection(connection))
	}()
	var actualDatabase string
	if err := connection.QueryRow(ctx, `SELECT current_database()`).Scan(&actualDatabase); err != nil {
		return fmt.Errorf("verify restore target database")
	}
	if actualDatabase != expectedDatabase {
		return fmt.Errorf("connected restore target does not match backup metadata")
	}
	var objectCount int64
	if err := connection.QueryRow(ctx, `
SELECT
    (SELECT count(*)
     FROM pg_catalog.pg_class c
     JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
       AND n.nspname NOT LIKE 'pg_toast%'
       AND c.relkind IN ('r','p','S','v','m','f')) +
    (SELECT count(*)
     FROM pg_catalog.pg_proc p
     JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
     WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
       AND n.nspname NOT LIKE 'pg_toast%') +
    (SELECT count(*)
     FROM pg_catalog.pg_type t
     JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
     WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
       AND n.nspname NOT LIKE 'pg_toast%'
       AND t.typtype IN ('c','d','e','r'))`).Scan(&objectCount); err != nil {
		return fmt.Errorf("inspect restore target")
	}
	if objectCount != 0 {
		return fmt.Errorf("restore target is not empty")
	}
	return nil
}

func inspectDatabase(args []string) (resultErr error) {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("database", "", "")
	owner := flags.String("owner", "", "")
	outputPath := flags.String("output", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !safeIdentifier(*database) || !safeIdentifier(*owner) || *outputPath == "" {
		return fmt.Errorf("invalid inspect arguments")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("database URL is required")
	}
	if err := requireDatabase(databaseURL, *database); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect for database inspection")
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeConnection(connection))
	}()
	if _, err := connection.Exec(ctx, `SET statement_timeout = '30s'`); err != nil {
		return fmt.Errorf("bound database inspection")
	}
	inspection, err := inspectSnapshot(ctx, connection, *database, *owner)
	if err != nil {
		return err
	}
	return writeJSONExclusive(*outputPath, inspection, 0o600)
}

type inspectionQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func inspectSnapshot(ctx context.Context, queryer inspectionQueryer, database, owner string) (databaseInspection, error) {
	rows, err := queryer.Query(ctx, `
SELECT tablename FROM pg_catalog.pg_tables
WHERE schemaname = 'public'
ORDER BY tablename`)
	if err != nil {
		return databaseInspection{}, fmt.Errorf("list database tables")
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return databaseInspection{}, fmt.Errorf("scan database table")
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return databaseInspection{}, fmt.Errorf("iterate database tables")
	}
	rows.Close()
	var inspectedAt time.Time
	if err := queryer.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&inspectedAt); err != nil {
		return databaseInspection{}, fmt.Errorf("read database inspection clock")
	}
	inspection := databaseInspection{
		FormatVersion: artifactVersion, Database: database, ExpectedOwner: owner,
		InspectedAt: inspectedAt.UTC(), TableRows: make(map[string]int64, len(tables)),
	}
	for _, table := range tables {
		query := `SELECT count(*) FROM ` + pgx.Identifier{"public", table}.Sanitize()
		var count int64
		if err := queryer.QueryRow(ctx, query).Scan(&count); err != nil {
			return databaseInspection{}, fmt.Errorf("count database table")
		}
		inspection.TableRows[table] = count
	}
	ownerRows, err := queryer.Query(ctx, `
SELECT c.relname
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_roles r ON r.oid = c.relowner
WHERE n.nspname = 'public'
  AND c.relkind IN ('r','p','S','v','m','f')
  AND r.rolname <> $1
ORDER BY c.relname`, owner)
	if err != nil {
		return databaseInspection{}, fmt.Errorf("inspect database object owners")
	}
	for ownerRows.Next() {
		var object string
		if err := ownerRows.Scan(&object); err != nil {
			ownerRows.Close()
			return databaseInspection{}, fmt.Errorf("scan database owner violation")
		}
		inspection.OwnerViolations = append(inspection.OwnerViolations, object)
	}
	if err := ownerRows.Err(); err != nil {
		ownerRows.Close()
		return databaseInspection{}, fmt.Errorf("iterate database owner violations")
	}
	ownerRows.Close()
	return inspection, nil
}

func compareInspections(args []string) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourcePath := flags.String("source", "", "")
	targetPath := flags.String("target", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *sourcePath == "" || *targetPath == "" {
		return fmt.Errorf("invalid compare arguments")
	}
	var source, target databaseInspection
	if err := readJSON(*sourcePath, &source); err != nil {
		return err
	}
	if err := readJSON(*targetPath, &target); err != nil {
		return err
	}
	if source.FormatVersion != artifactVersion || target.FormatVersion != artifactVersion ||
		source.Database != target.Database || source.ExpectedOwner != target.ExpectedOwner ||
		len(source.OwnerViolations) != 0 || len(target.OwnerViolations) != 0 ||
		!equalTableRows(source.TableRows, target.TableRows) {
		return fmt.Errorf("restored database inspection mismatch")
	}
	return nil
}

func databaseEnvironment(databaseURL string) ([]string, error) {
	parsed, database, password, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	environment := make([]string, 0, len(os.Environ())+10)
	blocked := []string{
		"DATABASE_URL=", "PGHOST=", "PGPORT=", "PGUSER=", "PGPASSWORD=", "PGDATABASE=",
		"PGSSLMODE=", "PGSSLROOTCERT=", "PGSSLCERT=", "PGSSLKEY=",
	}
	for _, item := range os.Environ() {
		keep := true
		for _, prefix := range blocked {
			if strings.HasPrefix(item, prefix) {
				keep = false
				break
			}
		}
		if keep {
			environment = append(environment, item)
		}
	}
	environment = append(environment,
		"PGHOST="+parsed.Hostname(),
		"PGPORT="+port,
		"PGUSER="+parsed.User.Username(),
		"PGPASSWORD="+password,
		"PGDATABASE="+database,
	)
	for query, environmentName := range map[string]string{
		"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT",
		"sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY",
	} {
		if value := parsed.Query().Get(query); value != "" {
			environment = append(environment, environmentName+"="+value)
		}
	}
	return environment, nil
}

func requireDatabase(databaseURL, expected string) error {
	_, actual, _, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("database URL does not match requested database")
	}
	return nil
}

func parseDatabaseURL(databaseURL string) (*url.URL, string, string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Hostname() == "" || parsed.User == nil || parsed.User.Username() == "" ||
		strings.TrimPrefix(parsed.EscapedPath(), "/") == "" {
		return nil, "", "", fmt.Errorf("database URL is invalid")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		return nil, "", "", fmt.Errorf("database URL password is required")
	}
	database, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !safeIdentifier(database) || strings.Contains(database, "/") {
		return nil, "", "", fmt.Errorf("database URL name is invalid")
	}
	return parsed, database, password, nil
}

func readRecipient(path string) (age.Recipient, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup recipient")
	}
	recipient, err := age.ParseX25519Recipient(strings.TrimSpace(string(value)))
	if err != nil {
		return nil, fmt.Errorf("parse backup recipient")
	}
	return recipient, nil
}

func readIdentity(path string) (age.Identity, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup identity")
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(value)))
	if err != nil {
		return nil, fmt.Errorf("parse backup identity")
	}
	return identity, nil
}

func writeExclusive(path string, body []byte, mode os.FileMode) error {
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create backup output")
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write backup output")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync backup output")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close backup output")
	}
	return os.Chmod(path, mode)
}

func writeJSONExclusive(path string, value any, mode os.FileMode) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode backup output")
	}
	body = append(body, '\n')
	return writeExclusive(path, body, mode)
}

func readJSON(path string, target any) (resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup metadata")
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeFile(file, "close backup metadata"))
	}()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode backup metadata")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("backup metadata contains trailing data")
	}
	return nil
}

func fileDigest(path string) (digest string, size int64, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open encrypted backup")
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeFile(file, "close encrypted backup"))
	}()
	hash := sha256.New()
	size, err = io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash encrypted backup")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func closeFile(file *os.File, failure string) error {
	if err := file.Close(); err != nil {
		return errors.New(failure)
	}
	return nil
}

func closeConnection(connection *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Close(ctx); err != nil {
		return errors.New("close database inspection connection")
	}
	return nil
}

func safeIdentifier(value string) bool {
	if len(value) < 3 || len(value) > 63 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func equalTableRows(left, right map[string]int64) bool {
	if len(left) != len(right) {
		return false
	}
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if right[key] != left[key] {
			return false
		}
	}
	return true
}

func postgresFailureCode(output string) string {
	normalized := strings.ToLower(output)
	switch {
	case strings.Contains(normalized, "password authentication failed"), strings.Contains(normalized, "no password supplied"):
		return "authentication_failed"
	case strings.Contains(normalized, "could not translate host name"), strings.Contains(normalized, "connection refused"), strings.Contains(normalized, "could not connect"):
		return "connection_failed"
	case strings.Contains(normalized, "does not exist"):
		return "database_missing"
	case strings.Contains(normalized, "permission denied"), strings.Contains(normalized, "must be owner"):
		return "permission_denied"
	case strings.Contains(normalized, "server version mismatch"):
		return "version_mismatch"
	default:
		return "command_failed"
	}
}

type boundedWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *boundedWriter) Write(body []byte) (int, error) {
	accepted := len(body)
	if w.remaining <= 0 {
		return accepted, nil
	}
	if len(body) > w.remaining {
		body = body[:w.remaining]
	}
	w.remaining -= len(body)
	_, err := w.buffer.Write(body)
	return accepted, err
}
