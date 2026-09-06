package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
	"github.com/goraffle/raffle/internal/raffle"
)

const (
	suspenseFrames = 30
	suspenseTotal  = 3400 * time.Millisecond
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

	result, err := s.engine.Draw(r.Context(), req.MustHave, req.AnyOf)
	if err != nil {
		if errors.Is(err, raffle.ErrNoEligible) {
			writeErr(w, http.StatusBadRequest, "no eligible entries")
			return
		}
		slogError(w, err, "draw")
		return
	}

	// Gather the eligible pool for the suspense animation, then broadcast
	// drama + result to all browsers. The HTTP response returns immediately.
	go s.broadcastDrama(result, req)

	writeJSON(w, http.StatusOK, result)
}

// broadcastDrama pushes one suspense frame (the full shuffled/unordered pool)
// followed by the winner, so every connected browser shares the same reveal.
func (s *Server) broadcastDrama(result *model.DrawResult, req model.DrawRequest) {
	names := s.eligibleNames(req)
	frames := []string{}
	for i := 0; i < suspenseFrames; i++ {
		if len(names) == 0 {
			break
		}
		frames = append(frames, names[rand.IntN(len(names))])
	}
	if msg, err := marshalEvent(EventSuspense, map[string]any{
		"names":     frames,
		"pool":      result.EligiblePool,
		"must_have": req.MustHave,
		"any_of":    req.AnyOf,
	}); err == nil {
		s.hub.Broadcast(msg)
	} else {
		slogErrorLogger(err, "marshal suspense event")
	}

	// Give the client time to run the animation, then resolve.
	time.Sleep(suspenseTotal)

	if msg, err := marshalEvent(EventWinner, result); err == nil {
		s.hub.Broadcast(msg)
	} else {
		slogErrorLogger(err, "marshal winner event")
	}
}

// eligibleNames returns the names of the eligible, non-excluded pool under the
// given tag filter, for reuse as suspense animation frames.
func (s *Server) eligibleNames(req model.DrawRequest) []string {
	cs, err := s.store.EligibleEntries(context.Background(), req.MustHave, req.AnyOf)
	if err != nil {
		slogErrorLogger(err, "fetch eligible pool for suspense")
		return nil
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name)
	}
	return names
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
