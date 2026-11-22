package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON sends a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode json", "err", err)
	}
}

// writeErr sends a {"error": msg} body with the given status.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Event is the WebSocket broadcast envelope.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

const (
	EventReveal = "reveal"
	// EventChanged is the one change event: it names the page regions a
	// change made stale, and browsers refresh exactly those.
	EventChanged = "changed"
)

// Region names a part of the page a browser refreshes on its own. The
// browser's region declarations (static/js/regions.js) use the same names.
type Region string

const (
	RegionTable      Region = "table"
	RegionHistory    Region = "history"
	RegionLastPick   Region = "result" // the last Pick on the Reel (#result)
	RegionFilter     Region = "filter"
	RegionTagPopover Region = "tag-popover"
	RegionListName   Region = "list-name"
)

// Changed is the "changed" event's data. Info, when set, is a message for
// the host (the Undo toast text).
type Changed struct {
	Regions []Region `json:"regions"`
	Info    string   `json:"info,omitempty"`
}

// marshalEvent JSON-encodes an Event for WS broadcast.
func marshalEvent(eventType string, data any) ([]byte, error) {
	return json.Marshal(Event{Type: eventType, Data: data})
}
