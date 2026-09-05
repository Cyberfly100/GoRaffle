package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/goraffle/raffle/migrations"
)

// Open connects to Postgres and pings until ready (with timeout).
func Open(databaseURL string) (*sql.DB, error) {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Retry ping a few times in case Postgres is still starting.
	var pingErr error
	for i := 0; i < 10; i++ {
		pingErr = conn.PingContext(ctx)
		if pingErr == nil {
			break
		}
		slog.Warn("waiting for database", "attempt", i+1, "err", pingErr)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database not reachable: %w", pingErr)
		case <-time.After(2 * time.Second):
		}
	}
	if pingErr != nil {
		return nil, pingErr
	}
	return conn, nil
}

// Migrate runs goose migrations against the embedded SQL files.
func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(db, "."); err != nil {
		return err
	}
	return nil
}
