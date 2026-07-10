// Package telemetry will hold events/sensor SQL stores.
// Phase 1 only exposes an optional Postgres ping for /readyz.
package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // Postgres driver (allow-listed)
)

// PingDB opens DSN briefly and runs SELECT 1. Empty DSN is a no-op success.
// No jobs schema is required (Phase 1).
func PingDB(ctx context.Context, dsn string) error {
	if dsn == "" {
		return nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("telemetry: open: %w", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("telemetry: ping: %w", err)
	}
	var one int
	if err := db.QueryRowContext(pingCtx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("telemetry: select: %w", err)
	}
	return nil
}
