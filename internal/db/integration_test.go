package db_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "github.com/lib/pq"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
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
	if err := store.Reset(context.Background()); err != nil {
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
		res, err := engine.Draw(ctx, model.TagFilter{})
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
	res, _ := engine.Draw(ctx, model.TagFilter{})
	if res.Winner.Name != "high" && res.Winner.Name != "low" {
		t.Fatalf("unexpected winner %s", res.Winner.Name)
	}
	// The winner now has 1; the other is the sole min (0), so it must win next.
	expected := map[string]string{"high": "low", "low": "high"}[res.Winner.Name]
	res2, err := engine.Draw(ctx, model.TagFilter{})
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
	res, err := engine.Draw(ctx, model.TagFilter{MustHave: []string{"VIP", "TeamA"}})
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
		store.Reset(ctx) // zero counts so everyone is always eligible
		res, err := engine.Draw(ctx, model.TagFilter{AnyOf: []string{"VIP"}})
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
	_, err := engine.Draw(ctx, model.TagFilter{})
	if err == nil {
		t.Fatal("expected error on empty pool")
	}
}

func TestUndo(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	store.AddEntry(ctx, nil, "x")
	res, _ := engine.Draw(ctx, model.TagFilter{})
	if res.Winner.Name != "x" {
		t.Fatal("expected x")
	}
	picks, _ := store.ListHistory(ctx)
	if len(picks) != 1 {
		t.Fatalf("history length = %d, want 1", len(picks))
	}
	undone, err := store.UndoLastPick(ctx)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if undone.ID != res.PickID || undone.EntryName != "x" || undone.PickNumber != 1 {
		t.Errorf("undo reported %+v, want Pick %d (#1) of x", undone, res.PickID)
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

// addEntries adds named Entries and returns their ids by name.
func addEntries(t *testing.T, store *db.Store, names ...string) map[string]int {
	t.Helper()
	ids := map[string]int{}
	for _, n := range names {
		e, err := store.AddEntry(context.Background(), nil, n)
		if err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
		ids[n] = e.ID
	}
	return ids
}

// setPickCount sets an Entry's Pick count directly (as a manual edit would).
func setPickCount(t *testing.T, store *db.Store, id, count int) {
	t.Helper()
	e, err := store.GetEntry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	e.PickCount = count
	if err := store.UpdateEntry(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

// Suspense names are sampled only from the Pot: Excluded, filtered-out and
// already-picked-this-Season Entries never appear on the Reel.
func TestSuspenseNamesComeFromPot(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	ids := addEntries(t, store, "ann", "ben", "cat", "dan", "eve", "out")
	for _, n := range []string{"ann", "ben", "cat", "dan", "eve"} {
		if err := store.SetEntryTags(ctx, nil, ids[n], []string{"in"}); err != nil {
			t.Fatal(err)
		}
	}
	eve, _ := store.GetEntry(ctx, ids["eve"])
	eve.Excluded = true
	if err := store.UpdateEntry(ctx, eve); err != nil {
		t.Fatal(err)
	}
	// ann and ben are one Pick ahead: the Pot is {cat, dan}.
	setPickCount(t, store, ids["ann"], 1)
	setPickCount(t, store, ids["ben"], 1)

	res, err := engine.Draw(ctx, model.TagFilter{MustHave: []string{"in"}})
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if len(res.SuspenseNames) == 0 {
		t.Fatal("no suspense names")
	}
	for _, n := range res.SuspenseNames {
		if n != "cat" && n != "dan" {
			t.Errorf("suspense name %q is not in the Pot {cat, dan}", n)
		}
	}
	if res.Winner.Name != "cat" && res.Winner.Name != "dan" {
		t.Errorf("winner %q is not in the Pot", res.Winner.Name)
	}
}

// With one Entry left in the Pot, the Reel repeats that name and it wins.
func TestLastStraggler(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	ids := addEntries(t, store, "ann", "ben", "cat")
	setPickCount(t, store, ids["ann"], 1)
	setPickCount(t, store, ids["ben"], 1)

	res, err := engine.Draw(ctx, model.TagFilter{})
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if res.Winner.Name != "cat" {
		t.Errorf("winner = %q, want the last straggler cat", res.Winner.Name)
	}
	if len(res.SuspenseNames) == 0 {
		t.Fatal("no suspense names")
	}
	for _, n := range res.SuspenseNames {
		if n != "cat" {
			t.Errorf("suspense name %q, want only cat", n)
		}
	}
}

// The Reveal's Eligible pool size counts the whole Eligible pool under the
// tag filter, not just the Pot.
func TestRevealEligiblePoolSize(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	ids := addEntries(t, store, "ann", "ben", "cat", "dan", "eve")
	for _, n := range []string{"ann", "ben", "cat", "dan"} {
		if err := store.SetEntryTags(ctx, nil, ids[n], []string{"in"}); err != nil {
			t.Fatal(err)
		}
	}
	dan, _ := store.GetEntry(ctx, ids["dan"])
	dan.Excluded = true
	if err := store.UpdateEntry(ctx, dan); err != nil {
		t.Fatal(err)
	}
	setPickCount(t, store, ids["ann"], 1)

	res, err := engine.Draw(ctx, model.TagFilter{MustHave: []string{"in"}})
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if res.EligiblePool != 3 {
		t.Errorf("eligible pool = %d, want 3 (ann, ben, cat)", res.EligiblePool)
	}
}

// The database itself rejects a second Pick with an existing Pick number.
func TestPickNumbersAreUnique(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	addEntries(t, store, "ann", "ben")
	res, err := engine.Draw(ctx, model.TagFilter{})
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	_, err = store.DB.ExecContext(ctx,
		`INSERT INTO picks (entry_id, pick_number) VALUES ($1, $2)`, res.Winner.ID, res.PickNumber)
	if err == nil {
		t.Fatal("inserting a duplicate Pick number succeeded, want it rejected")
	}
}

// concurrently runs n calls of fn spread over many goroutines that start
// together, and fails the test on any error.
func concurrently(t *testing.T, n int, fn func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := fn(i); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call: %v", err)
	}
}

// historyInOrder returns the History oldest first, failing unless the Pick
// numbers are exactly 1..len with no duplicates.
func historyInOrder(t *testing.T, store *db.Store) []model.Pick {
	t.Helper()
	picks, err := store.ListHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].PickNumber < picks[j].PickNumber })
	for i, p := range picks {
		if p.PickNumber != i+1 {
			nums := make([]int, len(picks))
			for k, q := range picks {
				nums[k] = q.PickNumber
			}
			t.Fatalf("Pick numbers = %v, want 1..%d with no duplicates", nums, len(picks))
		}
	}
	return picks
}

// With N Entries, k×N concurrent Draws pick everyone exactly k times, with
// Pick numbers exactly 1..k×N.
func TestConcurrentDrawsStayFair(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	const n, k = 8, 3
	ids := addEntries(t, store, "a", "b", "c", "d", "e", "f", "g", "h")

	concurrently(t, k*n, func(int) error {
		_, err := engine.Draw(ctx, model.TagFilter{})
		return err
	})

	picks := historyInOrder(t, store)
	if len(picks) != k*n {
		t.Errorf("history has %d Picks, want %d", len(picks), k*n)
	}
	for name, id := range ids {
		e, _ := store.GetEntry(ctx, id)
		if e.PickCount != k {
			t.Errorf("%s pick count = %d, want %d", name, e.PickCount, k)
		}
	}
}

// Replaying the History of concurrent Draws, every winner was in the Pot when
// its Pick was recorded: no Pick count ever pulls more than one ahead.
func TestConcurrentDrawsPickFromPot(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	addEntries(t, store, "a", "b", "c", "d", "e")

	concurrently(t, 23, func(int) error {
		_, err := engine.Draw(ctx, model.TagFilter{})
		return err
	})

	counts := map[int]int{}
	entries, _ := store.ListEntries(ctx)
	for _, e := range entries {
		counts[e.ID] = 0
	}
	for _, p := range historyInOrder(t, store) {
		for id, c := range counts {
			if c < counts[p.EntryID] {
				t.Fatalf("Pick %d went to %s with %d Picks while Entry %d had only %d",
					p.PickNumber, p.EntryName, counts[p.EntryID], id, c)
			}
		}
		counts[p.EntryID]++
	}
}

// Concurrent Draws mixed with Undo leave unique Pick numbers and Pick counts
// that agree with the History.
func TestConcurrentDrawsWithUndo(t *testing.T) {
	store, engine := setup(t)
	ctx := context.Background()
	addEntries(t, store, "a", "b", "c", "d")

	concurrently(t, 40, func(i int) error {
		if i%3 == 2 {
			if _, err := store.UndoLastPick(ctx); err != nil && !errors.Is(err, db.ErrNotFound) {
				return err
			}
			return nil
		}
		_, err := engine.Draw(ctx, model.TagFilter{})
		return err
	})

	fromHistory := map[int]int{}
	for _, p := range historyInOrder(t, store) {
		fromHistory[p.EntryID]++
	}
	entries, _ := store.ListEntries(ctx)
	for _, e := range entries {
		if e.PickCount != fromHistory[e.ID] {
			t.Errorf("%s pick count = %d, but History has %d Picks", e.Name, e.PickCount, fromHistory[e.ID])
		}
	}
}
