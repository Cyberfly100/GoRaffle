package handler

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/model"
	"github.com/goraffle/raffle/internal/raffle"
)

// pageData is what the index page and its partials render.
type pageData struct {
	Entries  []raffle.MarkedEntry
	Tags     []model.Tag
	History  []model.Pick
	LastPick *model.Pick
	// The Tag filter the table and filter bar are rendered for.
	model.TagFilter
	// ListName is rendered into the List name field and the export links so
	// the page arrives with the current name (no correction after load).
	ListName string
}

// tagPalette is a muted pastel set; index = hash(name) % len.
var tagPalette = []string{
	"tc-0", "tc-1", "tc-2", "tc-3",
	"tc-4", "tc-5", "tc-6", "tc-7",
	"tc-8", "tc-9", "tc-10", "tc-11",
	"tc-12", "tc-13", "tc-14", "tc-15",
	"tc-16", "tc-17", "tc-18", "tc-19",
	"tc-20", "tc-21", "tc-22", "tc-23",
}

// LoadTemplates parses all templates from the given filesystem (the templates/
// directory) and returns the set.
func LoadTemplates(sys fs.FS) (*template.Template, error) {
	funcs := template.FuncMap{
		"tagClass": func(name string) string {
			return tagPalette[fingerprint(name)%len(tagPalette)]
		},
		// revealTiming is the Reel's timing, rendered into the page once.
		"revealTiming": func() raffle.RevealTiming { return raffle.Timing },
		"formatTime": func(t time.Time) string {
			return t.Local().Format("02 Jan 2006 15:04")
		},
	}

	tmpl := template.New("").Funcs(funcs)
	patterns := []string{"layout.html", "index.html", "components/*.html"}
	for _, p := range patterns {
		matches, err := fs.Glob(sys, p)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if _, err := tmpl.ParseFS(sys, m); err != nil {
				return nil, err
			}
		}
	}
	if len(tmpl.Templates()) == 0 {
		return nil, fmt.Errorf("no templates found in embedded filesystem")
	}
	return tmpl, nil
}

func fingerprint(s string) int {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return int(h & 0x7fffffff)
}

func (s *Server) pageIndex(w http.ResponseWriter, r *http.Request) {
	data, err := s.buildPageData(r.Context())
	if err != nil {
		slogError(w, err, "build page data")
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		slogErrorLogger(err, "render index")
	}
}

func (s *Server) partialTable(w http.ResponseWriter, r *http.Request) {
	cs, err := s.engine.MarkEntries(r.Context(), tagFilterOf(r))
	if err != nil {
		slogError(w, err, "mark entries")
		return
	}
	renderNamed(w, s, "partial:entry_table", pageData{Entries: cs})
}

func (s *Server) partialHistory(w http.ResponseWriter, r *http.Request) {
	picks, err := s.store.ListHistory(r.Context())
	if err != nil {
		slogError(w, err, "list history")
		return
	}
	renderNamed(w, s, "partial:history", pageData{
		History:  picks,
		LastPick: lastPickPtr(picks),
	})
}

func (s *Server) partialResult(w http.ResponseWriter, r *http.Request) {
	last, err := s.store.LastPick(r.Context())
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		slogError(w, err, "last pick")
		return
	}
	var pick *model.Pick
	if err == nil {
		pick = &last
	}
	renderNamed(w, s, "partial:result", pageData{LastPick: pick})
}

func (s *Server) partialFilter(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		slogError(w, err, "list tags")
		return
	}
	renderNamed(w, s, "partial:filter_bar", pageData{
		Tags:      tags,
		TagFilter: tagFilterOf(r),
	})
}

// tagFilterOf reads the Tag filter from the must_have and any_of query params.
func tagFilterOf(r *http.Request) model.TagFilter {
	q := r.URL.Query()
	return model.TagFilter{MustHave: q["must_have"], AnyOf: q["any_of"]}
}

func (s *Server) partialTagPopover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	assigned, err := s.store.GetEntryTags(r.Context(), id)
	if err != nil {
		slogError(w, err, "get entry tags")
		return
	}
	all, err := s.store.ListTags(r.Context())
	if err != nil {
		slogError(w, err, "list tags")
		return
	}
	assignSet := map[string]bool{}
	for _, t := range assigned {
		assignSet[strings.ToLower(t.Name)] = true
	}
	var available []model.Tag
	for _, t := range all {
		if !assignSet[strings.ToLower(t.Name)] {
			available = append(available, t)
		}
	}
	renderNamed(w, s, "partial:tag_popover", pageData{Tags: available})
}

func renderNamed(w http.ResponseWriter, s *Server, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slogErrorLogger(err, "render template "+name)
	}
}

func (s *Server) buildPageData(ctx context.Context) (pageData, error) {
	// The page always starts with an empty Tag filter.
	cs, err := s.engine.MarkEntries(ctx, model.TagFilter{})
	if err != nil {
		return pageData{}, err
	}
	tags, err := s.store.ListTags(ctx)
	if err != nil {
		return pageData{}, err
	}
	picks, err := s.store.ListHistory(ctx)
	if err != nil {
		return pageData{}, err
	}
	listName, err := s.store.GetListName(ctx)
	if err != nil {
		return pageData{}, err
	}
	return pageData{
		ListName: listName,
		Entries:  cs,
		Tags:     tags,
		History:  picks,
		LastPick: lastPickPtr(picks),
	}, nil
}

func lastPickPtr(picks []model.Pick) *model.Pick {
	if len(picks) == 0 {
		return nil
	}
	p := picks[0]
	return &p
}
