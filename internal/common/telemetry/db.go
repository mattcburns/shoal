package telemetry

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // Postgres driver (allow-listed)
)

//go:embed schema.sql
var schemaFS embed.FS

// Open opens a Postgres database using the allow-listed pgx stdlib driver.
func Open(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("telemetry: empty DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("telemetry: open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)
	return db, nil
}

// Migrate applies the embedded schema (CREATE IF NOT EXISTS).
func Migrate(ctx context.Context, db *sql.DB) error {
	sqlBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("telemetry: read schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("telemetry: migrate: %w", err)
	}
	return nil
}

// OpenAndMigrate opens DSN and runs migrations.
func OpenAndMigrate(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("telemetry: ping: %w", err)
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
