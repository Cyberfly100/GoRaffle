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
	s.hub.Register(client)
	go client.WritePump()
	go client.ReadPump()
}
