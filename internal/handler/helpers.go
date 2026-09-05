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
	EventSuspense = "suspense"
	EventWinner   = "winner"
	EventUndo     = "undo"
	EventEntries  = "entries"
)

// marshalEvent JSON-encodes an Event for WS broadcast.
func marshalEvent(eventType string, data any) ([]byte, error) {
	return json.Marshal(Event{Type: eventType, Data: data})
}
