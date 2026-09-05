package db_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/raffle"
)

// testDB runs the integration tests against a dedicated "raffle_db_test"
// database (never the app's "raffle" nor the handler tests' "raffle_handler_test").
// That keeps these tests from clobbering the data used by the handler tests,
// since `go test ./...` runs package test binaries concurrently.
// TEST_DATABASE_URL is accepted only in the form of the raffle_db_test DSN and
// the database name is always forced to raffle_db_test.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgresql://user:pass@localhost:5432/raffle_db_test?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if dbName := strings.TrimPrefix(u.Path, "/"); dbName != "" && dbName != "raffle_db_test" {
		t.Fatalf("db integration tests must run against 'raffle_db_test', not '%s' "+
			"(the handler tests use 'raffle_handler_test'; the app uses 'raffle')", dbName)
	}

	// Connect to the maintenance database and recreate raffle_db_test.
	u2 := *u
	u2.Path = "/postgres"
	main, err := sql.Open("postgres", u2.String())
	if err != nil {
		t.Fatalf("open maintenance db: %v", err)
	}
	if err := main.Ping(); err != nil {
		t.Skipf("database not reachable (start postgres or set TEST_DATABASE_URL): %v", err)
	}
	defer main.Close()
	if _, err := main.Exec("DROP DATABASE IF EXISTS raffle_db_test WITH (FORCE)"); err != nil {
		t.Fatalf("drop raffle_db_test: %v", err)
	}
	if _, err := main.Exec("CREATE DATABASE raffle_db_test"); err != nil {
		t.Fatalf("create raffle_db_test: %v", err)
	}

	u.Path = "/raffle_db_test"
	conn, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping raffle_db_test: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// setup prepares a clean store/engine against the isolated test database.
func setup(t *testing.T) (*db.Store, *raffle.Engine) {
	t.Helper()
	conn := testDB(t)
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn)
	engine := raffle.NewEngine(store)
	if err := store.ResetScores(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := store.ClearEntries(context.Background()); err != nil {
		t.Fatalf("clear: %v", err)
	}
	return store, engine
}

func TestDrawFairness(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	names := []string{"a", "b", "c", "d"}
	for _, n := range names {
		if _, err := store.AddEntry(ctx, nil, n); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	seen := map[string]int{}
	for i := 0; i < len(names); i++ {
		res, err := engine.Draw(ctx, nil, nil)
		if err != nil {
			t.Fatalf("draw %d: %v", i, err)
		}
		seen[res.Winner.Name]++
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("after first round, %s picked %d times, want 1", name, seen[name])
		}
	}
}

func TestDrawMinCountPool(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	a, _ := store.AddEntry(ctx, nil, "high")
	b, _ := store.AddEntry(ctx, nil, "low")
	// First draw: both at min (0); winner must be one of them.
	res, _ := engine.Draw(ctx, nil, nil)
	if res.Winner.Name != "high" && res.Winner.Name != "low" {
		t.Fatalf("unexpected winner %s", res.Winner.Name)
	}
	// The winner now has 1; the other is the sole min (0), so it must win next.
	expected := map[string]string{"high": "low", "low": "high"}[res.Winner.Name]
	res2, err := engine.Draw(ctx, nil, nil)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if res2.Winner.Name != expected {
		t.Errorf("min-count pick = %s, want %s", res2.Winner.Name, expected)
	}
	_ = a
	_ = b
}

func TestDrawTagFilterAND(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	alice, _ := store.AddEntry(ctx, nil, "alice")
	bob, _ := store.AddEntry(ctx, nil, "bob")
	_ = store.SetEntryTags(ctx, nil, alice.ID, []string{"VIP", "TeamA"})
	_ = store.SetEntryTags(ctx, nil, bob.ID, []string{"VIP"})
	res, err := engine.Draw(ctx, []string{"VIP", "TeamA"}, nil)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if res.Winner.Name != "alice" {
		t.Errorf("AND filter winner = %s, want alice", res.Winner.Name)
	}
}

func TestDrawTagFilterOR(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	alice, _ := store.AddEntry(ctx, nil, "alice")
	bob, _ := store.AddEntry(ctx, nil, "bob")
	carol, _ := store.AddEntry(ctx, nil, "carol")
	_ = store.SetEntryTags(ctx, nil, alice.ID, []string{"VIP"})
	_ = store.SetEntryTags(ctx, nil, bob.ID, []string{"VIP"})
	winnerSet := map[string]bool{}
	for i := 0; i < 30; i++ {
		store.ResetScores(ctx) // zero counts so everyone is always eligible
		res, err := engine.Draw(ctx, nil, []string{"VIP"})
		if err != nil {
			t.Fatalf("draw %d: %v", i, err)
		}
		winnerSet[res.Winner.Name] = true
	}
	if winnerSet["carol"] {
		t.Error("OR filter selected carol (has no VIP tag)")
	}
	if len(winnerSet) == 0 {
		t.Error("OR filter never selected")
	}
	_ = carol
}

func TestDrawNoEligible(t *testing.T) {
	_, engine := setup(t)
	ctx := context.Background()
	_, err := engine.Draw(ctx, nil, nil)
	if err == nil {
		t.Fatal("expected error on empty pool")
	}
}

func TestUndo(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	store.AddEntry(ctx, nil, "x")
	res, _ := engine.Draw(ctx, nil, nil)
	if res.Winner.Name != "x" {
		t.Fatal("expected x")
	}
	picks, _ := store.ListHistory(ctx)
	if len(picks) != 1 {
		t.Fatalf("history length = %d, want 1", len(picks))
	}
	if err := store.UndoLastPick(ctx); err != nil {
		t.Fatalf("undo: %v", err)
	}
	picks, _ = store.ListHistory(ctx)
	if len(picks) != 0 {
		t.Errorf("history after undo = %d, want 0", len(picks))
	}
	c, _ := store.GetEntry(ctx, 1) // id 1 from insertion
	if c.PickCount != 0 {
		t.Errorf("pick_count after undo = %d, want 0", c.PickCount)
	}
}
