package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// changedEvent is the one change event every mutating endpoint broadcasts.
type changedEvent struct {
	Regions []string `json:"regions"`
	Info    string   `json:"info"`
}

// listen connects a browser's websocket and waits for the hub to register it.
func listen(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	time.Sleep(50 * time.Millisecond)
	return conn
}

// changesSeen collects every change event that arrives within a short
// window. Reveal events (the Draw's own broadcast) are not change events.
func changesSeen(t *testing.T, conn *websocket.Conn) []changedEvent {
	t.Helper()
	var seen []changedEvent
	for {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return seen // quiet for the window: nothing more is coming
		}
		var evt struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &evt); err != nil {
			t.Fatalf("event JSON: %v (%s)", err, msg)
		}
		switch evt.Type {
		case "reveal":
			continue
		case "changed":
			var c changedEvent
			if err := json.Unmarshal(evt.Data, &c); err != nil {
				t.Fatalf("changed event data: %v (%s)", err, msg)
			}
			seen = append(seen, c)
		default:
			t.Errorf("unexpected event type %q (%s)", evt.Type, msg)
		}
	}
}

func addEntryID(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	var e struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(postJSON(t, ts, "/api/entries", `{"name":"`+name+`"}`), &e); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	return itoa(e.ID)
}

func tagID(t *testing.T, ts *httptest.Server, entryID, tag string) string {
	t.Helper()
	var tags []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(postJSON(t, ts, "/api/entries/"+entryID+"/tags", `{"name":"`+tag+`"}`), &tags); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	for _, tg := range tags {
		if tg.Name == tag {
			return itoa(tg.ID)
		}
	}
	t.Fatalf("tag %q not on entry", tag)
	return ""
}

// Every mutating endpoint broadcasts exactly one "changed" event naming the
// regions it made stale.
func TestEveryChangeBroadcastsOneChangedEvent(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, ts *httptest.Server) string
		act     func(t *testing.T, ts *httptest.Server, id string)
		regions []string
	}{
		{
			name:    "add an Entry",
			act:     func(t *testing.T, ts *httptest.Server, _ string) { postJSON(t, ts, "/api/entries", `{"name":"Ada"}`) },
			regions: []string{"table"},
		},
		{
			name:  "rename an Entry",
			setup: func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act: func(t *testing.T, ts *httptest.Server, id string) {
				putJSON(t, ts, "/api/entries/"+id, `{"name":"Grace"}`)
			},
			regions: []string{"table", "history", "result"},
		},
		{
			name:  "change a Pick count",
			setup: func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act: func(t *testing.T, ts *httptest.Server, id string) {
				putJSON(t, ts, "/api/entries/"+id, `{"pick_count":3}`)
			},
			regions: []string{"table"},
		},
		{
			name:  "exclude an Entry",
			setup: func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act: func(t *testing.T, ts *httptest.Server, id string) {
				putJSON(t, ts, "/api/entries/"+id, `{"excluded":true}`)
			},
			regions: []string{"table"},
		},
		{
			name:    "delete an Entry",
			setup:   func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act:     func(t *testing.T, ts *httptest.Server, id string) { reqJSON(t, ts, "DELETE", "/api/entries/"+id, "") },
			regions: []string{"table", "history", "result"},
		},
		{
			name:    "clear the List",
			setup:   func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act:     func(t *testing.T, ts *httptest.Server, _ string) { reqJSON(t, ts, "DELETE", "/api/entries", "") },
			regions: []string{"table", "history", "result", "tag-popover"},
		},
		{
			name:  "add a Tag",
			setup: func(t *testing.T, ts *httptest.Server) string { return addEntryID(t, ts, "Ada") },
			act: func(t *testing.T, ts *httptest.Server, id string) {
				postJSON(t, ts, "/api/entries/"+id+"/tags", `{"name":"VIP"}`)
			},
			regions: []string{"table", "filter", "tag-popover"},
		},
		{
			name: "remove a Tag",
			setup: func(t *testing.T, ts *httptest.Server) string {
				id := addEntryID(t, ts, "Ada")
				return id + "/tags/" + tagID(t, ts, id, "VIP")
			},
			act: func(t *testing.T, ts *httptest.Server, path string) {
				reqJSON(t, ts, "DELETE", "/api/entries/"+path, "")
			},
			regions: []string{"table", "tag-popover"},
		},
		{
			name: "Reset",
			setup: func(t *testing.T, ts *httptest.Server) string {
				addEntryID(t, ts, "Ada")
				postJSON(t, ts, "/api/draw", `{}`)
				return ""
			},
			act:     func(t *testing.T, ts *httptest.Server, _ string) { postJSON(t, ts, "/api/reset", "") },
			regions: []string{"table", "history", "result"},
		},
		{
			name: "import a List",
			act: func(t *testing.T, ts *httptest.Server, _ string) {
				postJSON(t, ts, "/api/import", `{"list_name":"Club","entries":[{"name":"Ada","tags":["VIP"]}]}`)
			},
			// An Import replaces the whole List: every region is stale.
			regions: []string{"table", "history", "result", "filter", "tag-popover", "list-name"},
		},
		{
			name:    "rename the List",
			act:     func(t *testing.T, ts *httptest.Server, _ string) { putJSON(t, ts, "/api/list-name", `{"name":"Club"}`) },
			regions: []string{"list-name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServer(t)
			var arg string
			if tc.setup != nil {
				arg = tc.setup(t, ts)
			}
			conn := listen(t, ts)
			tc.act(t, ts, arg)
			seen := changesSeen(t, conn)
			if len(seen) != 1 {
				t.Fatalf("saw %d changed events, want exactly 1: %+v", len(seen), seen)
			}
			assertRegions(t, seen[0].Regions, tc.regions)
		})
	}
}

// Undo names every region it made stale and still carries the Undo info
// text for the toast.
func TestUndoBroadcastsOneChangedEventWithInfo(t *testing.T) {
	ts := newTestServer(t)
	addEntryID(t, ts, "Ada")
	postJSON(t, ts, "/api/draw", `{}`)
	conn := listen(t, ts)

	postJSON(t, ts, "/api/undo", "")

	seen := changesSeen(t, conn)
	if len(seen) != 1 {
		t.Fatalf("saw %d changed events, want exactly 1: %+v", len(seen), seen)
	}
	assertRegions(t, seen[0].Regions, []string{"table", "history", "result"})
	if seen[0].Info != "Removed last entry: Ada" {
		t.Errorf("info = %q, want the Undo toast text", seen[0].Info)
	}
}

func assertRegions(t *testing.T, got, want []string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("stale regions = %v, want %v", got, want)
	}
}
