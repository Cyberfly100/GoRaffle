package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/goraffle/raffle/migrations"
)

// emptyTestDB recreates raffle_db_test like testDB, then rolls every
// migration back so a test can migrate step by step from an empty schema.
func emptyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn := testDB(t)
	t.Cleanup(func() { conn.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.DownTo(conn, ".", 0); err != nil {
		t.Fatalf("roll back migrations: %v", err)
	}
	return conn
}

// A database deployed before Pick numbers were unique can hold duplicates.
// Migrating it must keep every Pick, renumber them 1..n in the order they
// were picked, and from then on reject a duplicate Pick number.
func TestMigrateRenumbersDuplicatePickNumbers(t *testing.T) {
	conn := emptyTestDB(t)
	ctx := context.Background()

	if err := goose.UpTo(conn, ".", 1); err != nil {
		t.Fatalf("migrate to version 1: %v", err)
	}
	var entryID int
	if err := conn.QueryRowContext(ctx, `SELECT id FROM entries ORDER BY id LIMIT 1`).Scan(&entryID); err != nil {
		t.Fatalf("seeded entry: %v", err)
	}
	// Two Picks share number 2, as concurrent Draws could produce.
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO picks (entry_id, pick_number, picked_at) VALUES
		  ($1, 1, '2026-01-01 10:00'),
		  ($1, 2, '2026-01-01 10:01'),
		  ($1, 2, '2026-01-01 10:02'),
		  ($1, 3, '2026-01-01 10:03')`, entryID); err != nil {
		t.Fatalf("insert legacy picks: %v", err)
	}

	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	rows, err := conn.QueryContext(ctx, `SELECT pick_number FROM picks ORDER BY picked_at, id`)
	if err != nil {
		t.Fatalf("read picks: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, n)
	}
	want := []int{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("pick numbers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pick numbers = %v, want %v", got, want)
		}
	}

	if _, err := conn.ExecContext(ctx,
		`INSERT INTO picks (entry_id, pick_number) VALUES ($1, 4)`, entryID); err == nil {
		t.Fatal("inserting a duplicate Pick number succeeded after migrating, want a unique violation")
	}
}
