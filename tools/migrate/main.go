package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dir     = flag.String("dir", env("MIGRATIONS_DIR", ""), "directory containing goose SQL migrations")
		command = flag.String("command", env("MIGRATION_COMMAND", "status"), "goose command: status, up, up-by-one, down, version")
		timeout = flag.Duration("timeout", 30*time.Second, "migration command timeout")
	)
	flag.Parse()

	if *dir == "" {
		return fmt.Errorf("migrations dir is required")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if err := goose.RunContext(ctx, *command, db, *dir); err != nil {
		return fmt.Errorf("run goose %s: %w", *command, err)
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
