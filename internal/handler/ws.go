package handler

import (
	"log/slog"
	"net/http"

	"github.com/goraffle/raffle/internal/ws"
)

func (s *Server) wsUpgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("websocket upgrade failed", "err", err)
		return
	}
	client := ws.NewClient(s.hub, conn)
	// A browser (re)connecting during a Reveal plays it too.
	if msg := s.lastReveal.replay(r.Context(), s.store.PickStands); msg != nil {
		client.Send(msg)
	}
	s.hub.Register(client)
	go client.WritePump()
	go client.ReadPump()
}
