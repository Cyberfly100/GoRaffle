package handler

import (
	"context"
	"net/http"
	"time"
)

// health answers whether the app and its database are reachable.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"db": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"db": true})
}
