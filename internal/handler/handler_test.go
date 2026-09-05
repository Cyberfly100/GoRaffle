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

// newTestServer spins up a full HTTP server over the handler package. It runs
// against its own "raffle_handler_test" database (NOT the db package's scratch
// "raffle_db_test" database, which the db integration tests recreate
// concurrently under `go test ./...`, nor the app's live "raffle" database).
// A differently-named TEST_DATABASE_URL fails fast instead of colliding.
func newTestServer(t *testing.T) *httptest.Server {
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
	if err := store.ResetScores(ctx); err != nil {
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

	// Reset scores then clear entries.
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

	// CSV export has a header.
	assertBodyContains(t, ts, "/api/export?format=csv", "name")
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

// WS: a connected browser receives the change broadcast over the websocket.
func TestWebSocketBroadcast(t *testing.T) {
	ts := newTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	// Give the hub event loop a moment to register the client, then trigger
	// broadcasts through the API.
	time.Sleep(50 * time.Millisecond)
	postJSON(t, ts, "/api/reset", "")
	postJSON(t, ts, "/api/reset", "")

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 3; i++ {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read ws: %v", err)
		}
		var evt struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &evt); err != nil {
			t.Fatalf("event JSON: %v (%s)", err, msg)
		}
		if evt.Type == "entries" {
			return // got a live broadcast
		}
	}
	t.Error("no entries event received over websocket")
}

// Suspense: after a draw, browsers receive a "suspense" event whose names
// array drives the flicker animation.
func TestSuspenseNames(t *testing.T) {
	ts := newTestServer(t)

	// Add two tagged entries so the filtered pool has names.
	alice := postJSON(t, ts, "/api/entries", `{"name":"Alice"}`)
	var cA struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(alice, &cA); err != nil {
		t.Fatalf("add response: %v", err)
	}
	postJSON(t, ts, "/api/entries", `{"name":"Bob"}`)
	postJSON(t, ts, "/api/entries/"+itoa(cA.ID)+"/tags", `{"name":"VIP"}`)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	// Let the hub register the client before broadcasting.
	time.Sleep(100 * time.Millisecond)
	postJSON(t, ts, "/api/draw", `{"must_have":["VIP"],"any_of":[]}`)

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws: %v", err)
	}
	var evt struct {
		Type string `json:"type"`
		Data struct {
			Names []string `json:"names"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg, &evt); err != nil {
		t.Fatalf("suspense JSON: %v (%s)", err, msg)
	}
	if evt.Type != "suspense" {
		t.Fatalf("first event = %q, want suspense (%s)", evt.Type, msg)
	}
	if evt.Type == "suspense" && len(evt.Data.Names) == 0 {
		t.Error("suspense event carried empty names array")
	}
}

func TestHistoryExportCSV(t *testing.T) {
	ts := newTestServer(t)
	postJSON(t, ts, "/api/entries", `{"name":"Alice"}`)
	postJSON(t, ts, "/api/entries", `{"name":"Bob"}`)

	postJSON(t, ts, "/api/draw", `{}`)
	postJSON(t, ts, "/api/draw", `{}`)

	res := doReq(t, ts, "GET", "/api/export/history?format=csv", nil)
	if res.StatusCode != 200 {
		t.Fatalf("history csv = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 3 {
		t.Fatalf("history csv lines = %d, want header + 2 draws:\n%s", len(lines), body)
	}
	if lines[0] != "draw_id,pick_number,entry_id,entry_name,picked_at" {
		t.Errorf("csv header = %q", lines[0])
	}
	for _, line := range lines[1:] {
		cols := strings.Split(line, ",")
		if len(cols) != 5 {
			t.Errorf("csv row = %q, want 5 columns", line)
			continue
		}
		if cols[3] != "Alice" && cols[3] != "Bob" {
			t.Errorf("csv winner = %q, want Alice or Bob", cols[3])
		}
		if _, err := time.Parse(time.RFC3339, cols[4]); err != nil {
			t.Errorf("csv picked_at %q not RFC3339: %v", cols[4], err)
		}
	}

	// JSON export carries entry ids/names for stats joins.
	res = doReq(t, ts, "GET", "/api/export/history?format=json", nil)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	var picks []model.Pick
	if err := json.Unmarshal(body, &picks); err != nil {
		t.Fatalf("history json: %v (%s)", err, body)
	}
	if len(picks) != 2 {
		t.Errorf("history json length = %d, want 2", len(picks))
	}
	if picks[0].EntryID == 0 || picks[0].EntryName == "" {
		t.Errorf("history json pick lacks entry id/name: %+v", picks[0])
	}
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
