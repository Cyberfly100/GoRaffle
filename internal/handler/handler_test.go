package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"

	web "github.com/goraffle/raffle"
	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/handler"
	"github.com/goraffle/raffle/internal/model"
	"github.com/goraffle/raffle/internal/raffle"
	"github.com/goraffle/raffle/internal/ws"
)

var ctx = context.Background()

// newTestServer starts a full HTTP server over the handler package. It runs
// against its own "raffle_handler_test" database (NOT the db package's scratch
// "raffle_db_test" database, which the db integration tests recreate
// concurrently under `go test ./...`, nor the app's live "raffle" database).
// A differently-named TEST_DATABASE_URL fails fast instead of colliding.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerWith(t, nil)
}

// newTestServerWith is newTestServer with a hook to configure the Server.
func newTestServerWith(t *testing.T, configure func(*handler.Server)) *httptest.Server {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgresql://user:pass@localhost:5432/raffle_handler_test?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if dbName := strings.TrimPrefix(u.Path, "/"); dbName != "" && dbName != "raffle_handler_test" {
		t.Fatalf("handler tests must run against 'raffle_handler_test', not '%s' "+
			"(the db integration tests use 'raffle_db_test'; the app uses 'raffle')", dbName)
	}
	// Create the handler test database on first use.
	adminURL := *u
	adminURL.Path = "/postgres"
	admin, err := sql.Open("postgres", adminURL.String())
	if err != nil {
		t.Fatalf("open maintenance db: %v", err)
	}
	if err := admin.Ping(); err != nil {
		t.Skipf("database not reachable (start postgres or set TEST_DATABASE_URL): %v", err)
	}
	var exists bool
	if err := admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'raffle_handler_test')`).Scan(&exists); err != nil {
		t.Fatalf("check raffle_handler_test: %v", err)
	}
	if !exists {
		if _, err := admin.Exec("CREATE DATABASE raffle_handler_test"); err != nil {
			t.Fatalf("create raffle_handler_test: %v", err)
		}
	}
	admin.Close()

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Skipf("database not reachable (start postgres or set TEST_DATABASE_URL): %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := db.NewStore(conn)
	// Isolate each test from any leftover state.
	if err := store.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := store.ClearEntries(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}

	tmplFS, err := fs.Sub(web.FS, "templates")
	if err != nil {
		t.Fatalf("sub templates: %v", err)
	}
	tmpl, err := handler.LoadTemplates(tmplFS)
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	srv := handler.NewServer(store, raffle.NewEngine(store), hub, tmpl, web.FS)
	if configure != nil {
		configure(srv)
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

// With empty DB: index, entries ([]), history ([]), and the static assets.
func TestIndexAndEmptyState(t *testing.T) {
	ts := newTestServer(t)

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET / = %d", res.StatusCode)
	}
	for _, want := range []string{"Raffle", "Welcome to the draw", "Entries", "History", "must_have", "any_of"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("index missing %q", want)
		}
	}

	assertJSONBody(t, ts, "GET", "/api/entries", nil, 200, func(b []byte) {
		if strings.TrimSpace(string(b)) != "[]" {
			t.Errorf("entries = %q, want []", b)
		}
	})
	assertJSONBody(t, ts, "GET", "/api/history", nil, 200, func(b []byte) {
		if strings.TrimSpace(string(b)) != "[]" {
			t.Errorf("history = %q, want []", b)
		}
	})

	// static assets serve
	for _, path := range []string{"/static/css/style.css", "/static/js/app.js", "/static/js/ws.js"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
	}
	// The browser code is native ES modules: browsers refuse a module that is
	// not served with a JavaScript MIME type.
	for _, path := range []string{"/static/js/app.js", "/static/js/ws.js", "/static/js/reel.js", "/static/js/reel-plan.js", "/static/js/sound.js", "/static/js/confetti.js", "/static/js/regions.js", "/static/js/collapsible.js"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
			t.Errorf("GET %s = %d %q, want 200 text/javascript", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
}

// Full round trip: add, rename, tag, draw (filter), undo, reset, clear.
func TestEntryLifecycle(t *testing.T) {
	ts := newTestServer(t)

	// Add two entries.
	alice := postJSON(t, ts, "/api/entries", `{"name":"Alice"}`)
	var cA struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(alice, &cA); err != nil {
		t.Fatalf("add response: %v (%s)", err, alice)
	}
	postJSON(t, ts, "/api/entries", `{"name":"Bob"}`)

	// Rename Alice -> Alice2.
	putJSON(t, ts, "/api/entries/"+itoa(cA.ID), `{"name":"Alice2"}`)

	// Add a tag and confirm it appears in /api/tags, the popover partial,
	// and the rendered table (tag bubble + "add tag" button persist).
	postJSON(t, ts, "/api/entries/"+itoa(cA.ID)+"/tags", `{"name":"VIP"}`)
	assertBodyContains(t, ts, "/api/tags", `"VIP"`)
	assertBodyContains(t, ts, "/partials/tag-popover/"+itoa(cA.ID), "tag-new-form")
	table := bodyOf(t, ts, "/partials/table")
	assertBodyContainsRaw(t, table, "tag-bubble")
	assertBodyContainsRaw(t, table, "VIP")
	assertBodyContainsRaw(t, table, `data-action="open-tags"`)

	// Tag-filtered draw for a single entry (min-count pool is just Alice2).
	draw := postJSON(t, ts, "/api/draw", `{"must_have":["VIP"],"any_of":[]}`)
	var res struct {
		Winner struct {
			Name string `json:"name"`
		}
	}
	if err := json.Unmarshal(draw, &res); err != nil {
		t.Fatalf("draw response: %v (%s)", err, draw)
	}
	if res.Winner.Name != "Alice2" {
		t.Errorf("winner = %s, want Alice2", res.Winner.Name)
	}

	// History has exactly one pick now.
	assertJSONBody(t, ts, "GET", "/api/history", nil, 200, func(b []byte) {
		var picks []map[string]any
		if err := json.Unmarshal(b, &picks); err != nil {
			t.Fatalf("history unahash: %v", err)
		}
		if len(picks) != 1 {
			t.Errorf("history len = %d, want 1", len(picks))
		}
	})

	// Undo.
	undone := postJSON(t, ts, "/api/undo", "")
	assertBodyContainsRaw(t, undone, "Removed")
	assertJSONBody(t, ts, "GET", "/api/history", nil, 200, func(b []byte) {
		if strings.TrimSpace(string(b)) != "[]" {
			t.Errorf("history after undo = %q, want []", b)
		}
	})

	// Reset, then clear the Entries.
	postJSON(t, ts, "/api/reset", "")
	del := doReq(t, ts, "DELETE", "/api/entries", nil)
	if del.StatusCode != 200 {
		t.Fatalf("DELETE /api/entries = %d", del.StatusCode)
	}
	del.Body.Close()

	// Bad id -> 404.
	resp := doReq(t, ts, "PUT", "/api/entries/999999", strings.NewReader(`{"name":"x"}`))
	if resp.StatusCode != 404 {
		t.Errorf("PUT unknown id = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestDrawNoEligible(t *testing.T) {
	ts := newTestServer(t)
	res := doReq(t, ts, "POST", "/api/draw", strings.NewReader(`{}`))
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("draw on empty entries = %d, want 400 (%s)", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "no eligible") {
		t.Errorf("expected 'no eligible' error, got %s", body)
	}
}

// Undo never pushes a Pick count below zero (a hand-edited count of 0 stays
// 0), still removes the Pick, and keeps the History undoable; above zero it
// decrements by one as before.
func TestUndoPickCountFloor(t *testing.T) {
	ts := newTestServer(t)

	created := postJSON(t, ts, "/api/entries", `{"name":"Solo"}`)
	var c struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(created, &c); err != nil {
		t.Fatalf("add response: %v (%s)", err, created)
	}

	pickCount := func() int {
		body := bodyOf(t, ts, "/api/entries")
		var entries []struct {
			Name      string `json:"name"`
			PickCount int    `json:"pick_count"`
		}
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Fatalf("entries unmarshal: %v (%s)", err, body)
		}
		if len(entries) != 1 || entries[0].Name != "Solo" {
			t.Fatalf("entries = %s, want just Solo", body)
		}
		return entries[0].PickCount
	}
	historyLen := func() int {
		body := bodyOf(t, ts, "/api/history")
		var picks []map[string]any
		if err := json.Unmarshal(body, &picks); err != nil {
			t.Fatalf("history unmarshal: %v (%s)", err, body)
		}
		return len(picks)
	}
	undo := func() {
		undone := postJSON(t, ts, "/api/undo", "")
		assertBodyContainsRaw(t, undone, "Removed last entry: Solo")
	}

	for i := 0; i < 3; i++ {
		postJSON(t, ts, "/api/draw", `{}`)
	}
	if got := pickCount(); got != 3 {
		t.Fatalf("pick_count after 3 draws = %d, want 3", got)
	}

	// Above zero, Undo still decrements by one.
	putJSON(t, ts, "/api/entries/"+itoa(c.ID), `{"pick_count":5}`)
	undo()
	if got := pickCount(); got != 4 {
		t.Errorf("pick_count after undo above zero = %d, want 4", got)
	}
	if got := historyLen(); got != 2 {
		t.Errorf("history after undo = %d Picks, want 2", got)
	}

	// At zero, Undo removes the Pick and the count stays 0, never negative.
	putJSON(t, ts, "/api/entries/"+itoa(c.ID), `{"pick_count":0}`)
	undo()
	if got := pickCount(); got != 0 {
		t.Errorf("pick_count after undo at zero = %d, want 0 (never negative)", got)
	}
	if got := historyLen(); got != 1 {
		t.Errorf("history after undo at zero = %d Picks, want 1", got)
	}

	// The History stays undoable: the next Undo reaches the Pick before it.
	undo()
	if got := historyLen(); got != 0 {
		t.Errorf("history after second undo = %d Picks, want 0", got)
	}
	if got := pickCount(); got != 0 {
		t.Errorf("pick_count after second undo = %d, want 0", got)
	}

	// Undo past the beginning is rejected.
	res := doReq(t, ts, "POST", "/api/undo", nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("undo with no Picks = %d, want 400 (%s)", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "cannot undo any further") {
		t.Errorf("undo with no Picks said %s, want 'cannot undo any further'", body)
	}
}

func TestImportMerges(t *testing.T) {
	ts := newTestServer(t)

	// JSON import with names only.
	res := postJSON(t, ts, "/api/import", `{"entries":[{"name":"One"},{"name":"Two"}]}`)
	var r struct {
		Added   int `json:"added"`
		Updated int `json:"updated"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatalf("import response: %v (%s)", err, res)
	}
	if r.Added != 2 || r.Updated != 0 {
		t.Errorf("import added=%d updated=%d, want 2/0", r.Added, r.Updated)
	}

	// Re-import same names with a count -> upserts, 0 added.
	res = postJSON(t, ts, "/api/import", `{"entries":[{"name":"One","pick_count":7}]}`)
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatalf("re-import response: %v (%s)", err, res)
	}
	if r.Added != 0 || r.Updated != 1 {
		t.Errorf("re-import added=%d updated=%d, want 0/1", r.Added, r.Updated)
	}

	// Export includes the data.
	assertBodyContains(t, ts, "/api/export?format=json", `"pick_count":7`)

	// Import with tags flows through the transaction path (SetEntryTags).
	res = postJSON(t, ts, "/api/import", `{"entries":[{"name":"Three","tags":["VIP","TeamA"]}]}`)
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatalf("tagged import response: %v (%s)", err, res)
	}
	if r.Added != 1 {
		t.Errorf("tagged import added=%d, want 1", r.Added)
	}
	assertBodyContains(t, ts, "/api/entries", `"VIP"`)

	// Legacy pre-rename exports used the "contestants" key; keep accepting them.
	res = postJSON(t, ts, "/api/import", `{"contestants":[{"name":"Legacy"}]}`)
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatalf("legacy import response: %v (%s)", err, res)
	}
	if r.Added != 1 {
		t.Errorf("legacy-keyed import added=%d, want 1", r.Added)
	}
}

// An import whose JSON parses but carries zero rows must error, not silently
// report success with 0 added / 0 updated.
func TestImportEmptyJSONErrors(t *testing.T) {
	ts := newTestServer(t)
	for _, body := range []string{`{}`, `{"entries":[]}`, `[]`} {
		resp := doReq(t, ts, "POST", "/api/import", strings.NewReader(body))
		bodyRaw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("import %s = %d, want 400 (%s)", body, resp.StatusCode, bodyRaw)
		}
		if !strings.Contains(string(bodyRaw), "entries") && !strings.Contains(string(bodyRaw), "entry") {
			t.Errorf("import %s error should mention entries, got %s", body, bodyRaw)
		}
	}
}

// revealEvent is the one websocket event a Draw sends.
type revealEvent struct {
	Type string `json:"type"`
	Data struct {
		Winner struct {
			ID        int      `json:"id"`
			Name      string   `json:"name"`
			PickCount int      `json:"pick_count"`
			Tags      []string `json:"tags"`
		} `json:"winner"`
		PickID        int      `json:"pick_id"`
		PickNumber    int      `json:"pick_number"`
		MustHave      []string `json:"must_have"`
		EligiblePool  int      `json:"eligible_pool"`
		SuspenseNames []string `json:"suspense_names"`
	} `json:"data"`
}

func dialWS(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readEvents collects every websocket message that arrives within d.
func readEvents(t *testing.T, conn *websocket.Conn, d time.Duration) [][]byte {
	t.Helper()
	var msgs [][]byte
	_ = conn.SetReadDeadline(time.Now().Add(d))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return msgs
		}
		msgs = append(msgs, msg)
	}
}

// seedVIP adds Alice (with the VIP Tag) and Bob, returning Alice's id.
func seedVIP(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	alice := postJSON(t, ts, "/api/entries", `{"name":"Alice"}`)
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(alice, &a); err != nil {
		t.Fatalf("add response: %v", err)
	}
	postJSON(t, ts, "/api/entries", `{"name":"Bob"}`)
	postJSON(t, ts, "/api/entries/"+itoa(a.ID)+"/tags", `{"name":"VIP"}`)
	return a.ID
}

// A Draw sends exactly one Reveal event, at once, carrying the winner, the
// Pick, the Eligible pool and Suspense names from the Pot. Its content is the
// HTTP Draw response.
func TestDrawSendsOneReveal(t *testing.T) {
	ts := newTestServer(t)
	aliceID := seedVIP(t, ts)
	conn := dialWS(t, ts)
	// Let the hub register the client before broadcasting.
	time.Sleep(100 * time.Millisecond)

	sent := time.Now()
	body := postJSON(t, ts, "/api/draw", `{"must_have":["VIP"],"any_of":[]}`)

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, first, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("no event within 500ms of the Draw: %v", err)
	}
	if lag := time.Since(sent); lag > 500*time.Millisecond {
		t.Errorf("Reveal arrived %v after the Draw, want immediately", lag)
	}
	var evt revealEvent
	if err := json.Unmarshal(first, &evt); err != nil {
		t.Fatalf("event JSON: %v (%s)", err, first)
	}
	if evt.Type != "reveal" {
		t.Fatalf("event = %q, want reveal (%s)", evt.Type, first)
	}
	d := evt.Data
	if d.Winner.ID != aliceID || d.Winner.Name != "Alice" || d.Winner.PickCount != 1 {
		t.Errorf("winner = %+v, want Alice with 1 Pick", d.Winner)
	}
	if len(d.Winner.Tags) != 1 || d.Winner.Tags[0] != "VIP" {
		t.Errorf("winner Tags = %+v, want [VIP]", d.Winner.Tags)
	}
	if d.PickNumber != 1 || d.PickID == 0 {
		t.Errorf("pick_number = %d, pick_id = %d; want 1 and an id", d.PickNumber, d.PickID)
	}
	if d.EligiblePool != 1 {
		t.Errorf("eligible_pool = %d, want 1", d.EligiblePool)
	}
	if len(d.MustHave) != 1 || d.MustHave[0] != "VIP" {
		t.Errorf("must_have = %v, want [VIP]", d.MustHave)
	}
	if len(d.SuspenseNames) == 0 {
		t.Error("Reveal carried no Suspense names")
	}
	// Only Alice has the VIP Tag, so every name on the Reel is Alice.
	for _, n := range d.SuspenseNames {
		if n != "Alice" {
			t.Errorf("Suspense name %q is outside the Pot (Alice only)", n)
		}
	}

	var fromHTTP map[string]any
	if err := json.Unmarshal(body, &fromHTTP); err != nil {
		t.Fatalf("draw response JSON: %v (%s)", err, body)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(first, &env); err != nil {
		t.Fatalf("event JSON: %v", err)
	}
	if a, b := mustJSON(t, fromHTTP), mustJSON(t, env.Data); a != b {
		t.Errorf("HTTP Draw response differs from the Reveal event:\n http: %s\n   ws: %s", a, b)
	}

	// No separate winner event follows.
	if more := readEvents(t, conn, 2500*time.Millisecond); len(more) != 0 {
		t.Errorf("events after the Reveal: %s", more)
	}
}

// A browser that connects shortly after a Draw is sent that Reveal alone;
// one that connects after the replay window gets nothing.
func TestRevealReplayOnConnect(t *testing.T) {
	const window = 600 * time.Millisecond
	ts := newTestServerWith(t, func(s *handler.Server) { s.SetRevealReplayWindow(window) })
	seedVIP(t, ts)
	body := postJSON(t, ts, "/api/draw", `{}`)
	var drawn struct {
		PickID int `json:"pick_id"`
	}
	if err := json.Unmarshal(body, &drawn); err != nil {
		t.Fatalf("draw response: %v", err)
	}

	early := readEvents(t, dialWS(t, ts), 300*time.Millisecond)
	if len(early) != 1 {
		t.Fatalf("connecting within the window got %d events, want the one Reveal: %s", len(early), early)
	}
	var evt revealEvent
	if err := json.Unmarshal(early[0], &evt); err != nil {
		t.Fatalf("event JSON: %v", err)
	}
	if evt.Type != "reveal" || evt.Data.PickID != drawn.PickID {
		t.Errorf("replayed %s, want the Reveal of Pick %d", early[0], drawn.PickID)
	}

	time.Sleep(window)
	if late := readEvents(t, dialWS(t, ts), 300*time.Millisecond); len(late) != 0 {
		t.Errorf("connecting after the window got %s, want nothing", late)
	}
}

// Once the last Reveal's Pick is gone (Undo, Reset, Clear, or deleting the
// winning Entry), a connecting browser is not sent it.
func TestNoRevealReplayOncePickIsGone(t *testing.T) {
	for _, tc := range []struct{ name, method, path string }{
		{"undo", http.MethodPost, "/api/undo"},
		{"reset", http.MethodPost, "/api/reset"},
		{"clear", http.MethodDelete, "/api/entries"},
		{"delete winner", http.MethodDelete, "/api/entries/{winner}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServer(t)
			seedVIP(t, ts)
			var drawn revealEvent
			if err := json.Unmarshal(postJSON(t, ts, "/api/draw", `{}`), &drawn.Data); err != nil {
				t.Fatalf("draw response: %v", err)
			}
			path := strings.ReplaceAll(tc.path, "{winner}", itoa(drawn.Data.Winner.ID))
			reqJSON(t, ts, tc.method, path, "")
			for _, msg := range readEvents(t, dialWS(t, ts), 300*time.Millisecond) {
				if strings.Contains(string(msg), `"type":"reveal"`) {
					t.Errorf("replayed a Reveal after %s: %s", tc.name, msg)
				}
			}
		})
	}
}

// Deleting an Entry that did not win leaves the Reveal replayable.
func TestRevealReplaysAfterDeletingAnotherEntry(t *testing.T) {
	ts := newTestServer(t)
	aliceID := seedVIP(t, ts)
	postJSON(t, ts, "/api/draw", `{"must_have":["VIP"]}`) // Alice wins
	var bob int
	var entries []model.Entry
	if err := json.Unmarshal(bodyOf(t, ts, "/api/entries"), &entries); err != nil {
		t.Fatalf("entries: %v", err)
	}
	for _, e := range entries {
		if e.ID != aliceID {
			bob = e.ID
		}
	}
	reqJSON(t, ts, http.MethodDelete, "/api/entries/"+itoa(bob), "")
	got := readEvents(t, dialWS(t, ts), 300*time.Millisecond)
	if len(got) != 1 || !strings.Contains(string(got[0]), `"type":"reveal"`) {
		t.Errorf("connecting after deleting a non-winner got %s, want the Reveal", got)
	}
}

// A browser connecting while a Draw happens sees its Reveal exactly once:
// either replayed on connect or broadcast after, never missed.
func TestConnectDuringDrawNeverMissesReveal(t *testing.T) {
	ts := newTestServer(t)
	seedVIP(t, ts)
	for round := 0; round < 20; round++ {
		const clients = 8
		conns := make(chan *websocket.Conn, clients)
		var wg sync.WaitGroup
		for i := 0; i < clients; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
				if err != nil {
					t.Errorf("dial ws: %v", err)
					return
				}
				conns <- conn
			}()
		}
		var drawn revealEvent
		if err := json.Unmarshal(postJSON(t, ts, "/api/draw", `{}`), &drawn.Data); err != nil {
			t.Fatalf("draw response: %v", err)
		}
		wg.Wait()
		close(conns)
		for conn := range conns {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				for _, msg := range readEvents(t, conn, 150*time.Millisecond) {
					var evt revealEvent
					if json.Unmarshal(msg, &evt) == nil && evt.Type == "reveal" && evt.Data.PickID == drawn.Data.PickID {
						return
					}
				}
				t.Errorf("round %d: a browser connecting during the Draw missed its Reveal", round)
			}()
		}
		wg.Wait()
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestDBHealth(t *testing.T) {
	ts := newTestServer(t)
	assertJSONBody(t, ts, "GET", "/api/health", nil, 200, func(body []byte) {
		var h map[string]bool
		if err := json.Unmarshal(body, &h); err != nil {
			t.Fatalf("health JSON: %v (%s)", err, body)
		}
		if !h["db"] {
			t.Errorf("health db = %v, want true", h["db"])
		}
	})
}

// --- helpers ---------------------------------------------------------------

func itoa(n int) string {
	return strconv.Itoa(n)
}

func doReq(t *testing.T, ts *httptest.Server, method, path string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func postJSON(t *testing.T, ts *httptest.Server, path, body string) []byte {
	t.Helper()
	return reqJSON(t, ts, "POST", path, body)
}

func putJSON(t *testing.T, ts *httptest.Server, path, body string) []byte {
	t.Helper()
	return reqJSON(t, ts, "PUT", path, body)
}

func reqJSON(t *testing.T, ts *httptest.Server, method, path, body string) []byte {
	t.Helper()
	res := doReq(t, ts, method, path, strings.NewReader(body))
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		t.Fatalf("%s %s = %d (%s)", method, path, res.StatusCode, out)
	}
	return out
}

func assertBodyContains(t *testing.T, ts *httptest.Server, path, want string) {
	t.Helper()
	res := doReq(t, ts, "GET", path, nil)
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET %s = %d (%s)", path, res.StatusCode, out)
	}
	if !strings.Contains(string(out), want) {
		t.Errorf("GET %s missing %q\nbody: %s", path, want, out)
	}
}

func bodyOf(t *testing.T, ts *httptest.Server, path string) []byte {
	t.Helper()
	res := doReq(t, ts, "GET", path, nil)
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET %s = %d (%s)", path, res.StatusCode, out)
	}
	return out
}

func assertBodyContainsRaw(t *testing.T, body []byte, want string) {
	t.Helper()
	if !strings.Contains(string(body), want) {
		t.Errorf("body missing %q\nbody: %s", want, body)
	}
}

func assertJSONBody(t *testing.T, ts *httptest.Server, method, path string, body io.Reader, wantStatus int, check func([]byte)) {
	t.Helper()
	res := doReq(t, ts, method, path, body)
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != wantStatus {
		t.Fatalf("%s %s = %d (%s)", method, path, res.StatusCode, out)
	}
	check(out)
}

// The page carries the Reveal timing the server defines, and the Reel reads
// it from there instead of keeping its own copy.
func TestPageCarriesRevealTiming(t *testing.T) {
	ts := newTestServer(t)
	page := string(bodyOf(t, ts, "/"))
	const open = `<script type="application/json" id="reveal-timing">`
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("page has no %s", open)
	}
	rest := page[i+len(open):]
	var got raffle.RevealTiming
	if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &got); err != nil {
		t.Fatalf("reveal timing JSON: %v", err)
	}
	if want := (raffle.RevealTiming{SuspenseMS: 2000, LandingMS: 1800, LandingMaxMS: 3000, SettleMS: 420}); got != want {
		t.Errorf("page timing = %+v, want %+v", got, want)
	}

	reel := string(bodyOf(t, ts, "/static/js/reel.js"))
	if !strings.Contains(reel, `"reveal-timing"`) {
		t.Error("reel.js does not read the page's reveal-timing")
	}
	for _, own := range []string{"SUSPENSE_MS = 2", "LANDING_MS = 1", "SETTLE_MS = 4"} {
		if strings.Contains(reel, own) {
			t.Errorf("reel.js keeps its own timing constant %q", own)
		}
	}
}
