package handler

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/raffle"
	"github.com/goraffle/raffle/internal/ws"
)

type Server struct {
	store    *db.Store
	engine   *raffle.Engine
	hub      *ws.Hub
	tmpl     *template.Template
	staticFS fs.FS
	upgrader websocket.Upgrader
}

func NewServer(store *db.Store, engine *raffle.Engine, hub *ws.Hub, tmpl *template.Template, staticFS fs.FS) *Server {
	return &Server{
		store:    store,
		engine:   engine,
		hub:      hub,
		tmpl:     tmpl,
		staticFS: staticFS,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Routes returns the full HTTP mux for the application.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Pages
	mux.HandleFunc("GET /", s.pageIndex)
	mux.HandleFunc("GET /partials/table", s.partialTable)
	mux.HandleFunc("GET /partials/history", s.partialHistory)
	mux.HandleFunc("GET /partials/result", s.partialResult)
	mux.HandleFunc("GET /partials/filter", s.partialFilter)
	mux.HandleFunc("GET /partials/tag-popover/{id}", s.partialTagPopover)

	// API — entries
	mux.HandleFunc("GET /api/entries", s.listEntries)
	mux.HandleFunc("POST /api/entries", s.addEntry)
	mux.HandleFunc("DELETE /api/entries", s.clearEntries)
	mux.HandleFunc("PUT /api/entries/{id}", s.updateEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", s.deleteEntry)

	// API — tags
	mux.HandleFunc("GET /api/tags", s.listTags)
	mux.HandleFunc("GET /api/entries/{id}/tags", s.listEntryTags)
	mux.HandleFunc("POST /api/entries/{id}/tags", s.addEntryTag)
	mux.HandleFunc("DELETE /api/entries/{id}/tags/{tag_id}", s.deleteEntryTag)

	// API — draw / history
	mux.HandleFunc("POST /api/draw", s.draw)
	mux.HandleFunc("POST /api/undo", s.undo)
	mux.HandleFunc("POST /api/reset", s.resetScores)
	mux.HandleFunc("GET /api/history", s.history)

	// API — import / export
	mux.HandleFunc("GET /api/export", s.exportData)
	mux.HandleFunc("GET /api/export/history", s.exportHistory)
	mux.HandleFunc("POST /api/import", s.importData)
	mux.HandleFunc("GET /api/health", s.health)

	// WebSocket
	mux.HandleFunc("GET /ws", s.wsUpgrade)

	// Static
	staticSub, err := fs.Sub(s.staticFS, "static")
	if err != nil {
		panic("static files missing from embedded FS: " + err.Error())
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	return withLogging(mux)
}

// broadcast marshals a change event and fans it out over the hub.
func (s *Server) broadcast(eventType string, data any) {
	msg, err := marshalEvent(eventType, data)
	if err != nil {
		slog.Error("marshal broadcast event", "type", eventType, "err", err)
		return
	}
	s.hub.Broadcast(msg)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func slogErrorLogger(err error, what string) {
	slog.Error(what, "err", err)
}
