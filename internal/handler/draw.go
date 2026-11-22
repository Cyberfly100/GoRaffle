package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
	"github.com/goraffle/raffle/internal/raffle"
)

// suspenseTotal is how long the Suspense runs before the winner is revealed.
const suspenseTotal = 2000 * time.Millisecond

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

	writeJSON(w, http.StatusOK, reveal)

	// Every connected browser shares the same Reveal. The broadcast runs in
	// the background so the HTTP response returns immediately.
	go s.broadcastReveal(reveal)
}

// broadcastReveal pushes the Suspense (names from the Pot) followed, after
// the Suspense duration, by the winner.
func (s *Server) broadcastReveal(reveal *raffle.Reveal) {
	if msg, err := marshalEvent(EventSuspense, map[string]any{
		"names":     reveal.SuspenseNames,
		"pool":      reveal.EligiblePool,
		"must_have": reveal.MustHave,
		"any_of":    reveal.AnyOf,
	}); err == nil {
		s.hub.Broadcast(msg)
	} else {
		slogErrorLogger(err, "marshal suspense event")
	}

	// Give the client time to run the animation, then resolve.
	time.Sleep(suspenseTotal)

	if msg, err := marshalEvent(EventWinner, reveal); err == nil {
		s.hub.Broadcast(msg)
	} else {
		slogErrorLogger(err, "marshal winner event")
	}
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	last, err := s.store.LastPick(r.Context())
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "cannot undo any further")
			return
		}
		slogError(w, err, "last pick")
		return
	}
	if err := s.store.UndoLastPick(r.Context()); err != nil {
		slogError(w, err, "undo")
		return
	}

	info := "Removed last entry: " + last.EntryName
	if msg, err := marshalEvent(EventUndo, map[string]any{
		"info": info,
		"name": last.EntryName,
		"num":  last.PickNumber,
	}); err == nil {
		s.hub.Broadcast(msg)
	}

	writeJSON(w, http.StatusOK, map[string]string{"info": info})
}

func (s *Server) resetScores(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetScores(r.Context()); err != nil {
		slogError(w, err, "reset scores")
		return
	}
	if msg, err := marshalEvent(EventEntries, map[string]bool{"reset": true}); err == nil {
		s.hub.Broadcast(msg)
	}
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
