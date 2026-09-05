package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/goraffle/raffle/internal/db"
)

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	cs, err := s.store.ListEntries(r.Context())
	if err != nil {
		slogError(w, err, "list entries")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

type nameReq struct {
	Name string `json:"name"`
}

func (s *Server) addEntry(w http.ResponseWriter, r *http.Request) {
	var req nameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name must not be empty")
		return
	}
	c, err := s.store.AddEntry(r.Context(), nil, req.Name)
	if err != nil {
		slogError(w, err, "add entry")
		return
	}
	s.broadcast(EventEntries, c)
	writeJSON(w, http.StatusCreated, c)
}

type updateEntryReq struct {
	Name      *string `json:"name,omitempty"`
	PickCount *int    `json:"pick_count,omitempty"`
	Excluded  *bool   `json:"excluded,omitempty"`
}

func (s *Server) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req updateEntryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	c, err := s.store.GetEntry(r.Context(), id)
	if err != nil {
		handleNotFound(w, err)
		return
	}
	if req.Name != nil {
		c.Name = strings.TrimSpace(*req.Name)
	}
	if req.PickCount != nil {
		c.PickCount = *req.PickCount
	}
	if req.Excluded != nil {
		c.Excluded = *req.Excluded
	}
	if err := s.store.UpdateEntry(r.Context(), c); err != nil {
		handleNotFound(w, err)
		return
	}
	updated, err := s.store.GetEntry(r.Context(), id)
	if err != nil {
		slogError(w, err, "fetch updated entry")
		return
	}
	s.broadcast(EventEntries, updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeleteEntry(r.Context(), id); err != nil {
		handleNotFound(w, err)
		return
	}
	s.broadcast(EventEntries, map[string]int{"id": id})
	writeJSON(w, http.StatusOK, map[string]int{"id": id})
}

func (s *Server) clearEntries(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearEntries(r.Context()); err != nil {
		slogError(w, err, "clear entries")
		return
	}
	s.broadcast(EventEntries, map[string]bool{"cleared": true})
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// ---- Tags ----

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		slogError(w, err, "list tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) listEntryTags(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetEntry(r.Context(), id); err != nil {
		handleNotFound(w, err)
		return
	}
	tags, err := s.store.GetEntryTags(r.Context(), id)
	if err != nil {
		slogError(w, err, "list entry tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) addEntryTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req nameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "tag name must not be empty")
		return
	}
	if _, err := s.store.GetEntry(r.Context(), id); err != nil {
		handleNotFound(w, err)
		return
	}
	if err := s.store.AddTagToEntry(r.Context(), id, req.Name); err != nil {
		slogError(w, err, "add tag")
		return
	}
	tags, err := s.store.GetEntryTags(r.Context(), id)
	if err != nil {
		slogError(w, err, "list tags")
		return
	}
	s.broadcast(EventEntries, tags)
	writeJSON(w, http.StatusCreated, tags)
}

func (s *Server) deleteEntryTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tagID, err := pathID(r, "tag_id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.RemoveTagFromEntry(r.Context(), id, tagID); err != nil {
		handleNotFound(w, err)
		return
	}
	s.broadcast(EventEntries, map[string]int{"id": id, "tag_id": tagID})
	writeJSON(w, http.StatusOK, map[string]int{"id": id, "tag_id": tagID})
}

// ---- helpers ----

func pathID(r *http.Request, name string) (int, error) {
	raw := r.PathValue(name)
	if raw == "" {
		return 0, errors.New("missing id in path")
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("invalid id in path")
	}
	return id, nil
}

func handleNotFound(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	slogError(w, err, "database error")
}

func slogError(w http.ResponseWriter, err error, what string) {
	slogErrorLogger(err, what)
	writeErr(w, http.StatusInternalServerError, "internal error: "+what)
}
