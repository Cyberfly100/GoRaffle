package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
	"github.com/goraffle/raffle/internal/raffle"
)

func (s *Server) draw(w http.ResponseWriter, r *http.Request) {
	var req model.DrawRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.MustHave == nil {
		req.MustHave = []string{}
	}
	if req.AnyOf == nil {
		req.AnyOf = []string{}
	}

	reveal, err := s.engine.Draw(r.Context(), req.MustHave, req.AnyOf)
	if err != nil {
		if errors.Is(err, raffle.ErrNoEligible) {
			writeErr(w, http.StatusBadRequest, "no eligible entries")
			return
		}
		slogError(w, err, "draw")
		return
	}

	// Every connected browser plays the same Reveal, starting the moment the
	// event arrives; the Reel owns the pacing, so the server holds no timing.
	// The drawing browser gets the same Reveal in the response, so it can
	// play it without a websocket.
	s.broadcastReveal(reveal)
	writeJSON(w, http.StatusOK, reveal)
}

// broadcastReveal sends the one Reveal event to every browser.
func (s *Server) broadcastReveal(reveal *raffle.Reveal) {
	msg, err := marshalEvent(EventReveal, reveal)
	if err != nil {
		slogErrorLogger(err, "marshal reveal event")
		return
	}
	s.lastReveal.remember(msg)
	s.hub.Broadcast(msg)
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	last, err := s.store.UndoLastPick(r.Context())
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "cannot undo any further")
			return
		}
		slogError(w, err, "undo")
		return
	}
	s.lastReveal.forget()

	info := "Removed last entry: " + last.EntryName
	s.broadcastChanged(Changed{Regions: []Region{RegionTable, RegionHistory, RegionResult}, Info: info})

	writeJSON(w, http.StatusOK, map[string]string{"info": info})
}

func (s *Server) resetScores(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetScores(r.Context()); err != nil {
		slogError(w, err, "reset scores")
		return
	}
	s.lastReveal.forget()
	s.changed(RegionTable, RegionHistory, RegionResult)
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	picks, err := s.store.ListHistory(r.Context())
	if err != nil {
		slogError(w, err, "list history")
		return
	}
	writeJSON(w, http.StatusOK, picks)
}
