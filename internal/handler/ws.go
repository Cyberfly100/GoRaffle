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
	// Register before looking for a Reveal to replay, so a Draw broadcast in
	// between reaches this browser too. It may then get the same Reveal
	// twice; the browser dedupes by Pick id.
	s.hub.Register(client)
	go client.WritePump()
	go client.ReadPump()
	// A browser (re)connecting during a Reveal plays it too.
	if msg := s.lastReveal.replay(r.Context(), s.store.PickStands); msg != nil {
		s.hub.SendTo(client, msg)
	}
}
