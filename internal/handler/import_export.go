package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/listexchange"
)

// maxUpload caps the size of an imported file.
const maxUpload = 10 << 20

// queryFormat returns the ?format= value or def when absent.
func queryFormat(r *http.Request, def string) string {
	if f := r.URL.Query().Get("format"); f != "" {
		return f
	}
	return def
}

var filenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = filenameRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// writeAttachment sends a file download.
func writeAttachment(w http.ResponseWriter, contentType, filename string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if _, err := w.Write(body); err != nil {
		slogErrorLogger(err, "write "+filename)
	}
}

func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	format, err := listexchange.ParseFormat(queryFormat(r, "json"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.store.ReadList(r.Context())
	if err != nil {
		slogError(w, err, "read list for export")
		return
	}
	// The page may pass the List name it shows (for dynamic export links).
	if name := r.URL.Query().Get("name"); name != "" {
		list.Name = name
	}
	body, err := listexchange.Encode(list, format)
	if err != nil {
		slogError(w, err, "encode list for export")
		return
	}
	safeName := sanitizeFilename(list.Name)
	if safeName == "" {
		safeName = "raffle"
	}
	writeAttachment(w, format.ContentType(), safeName+"."+format.Extension(), body)
}

// importData reads an uploaded (multipart "file" field) or directly posted
// List file, decodes it and replaces the List with what it says, in one
// transaction. A direct request replaces at once: there is no confirmation
// flag, only the "skip missing Picks" and "remove unused Tags" options.
func (s *Server) importData(w http.ResponseWriter, r *http.Request) {
	body, hint, err := readUpload(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := listexchange.Decode(body, hint)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not parse import: "+err.Error())
		return
	}
	// FormValue reads the query string, and the multipart form's fields
	// once readUpload has parsed it.
	opts := db.ImportOptions{
		SkipMissingPicks: isTrue(r.FormValue("skip_missing_picks")),
		RemoveUnusedTags: isTrue(r.FormValue("remove_unused_tags")),
	}
	res, err := s.store.ImportList(r.Context(), list, opts)
	var missing *db.MissingEntriesError
	if errors.As(err, &missing) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":         missing.Error(),
			"missing_names": missing.Names,
			"missing_picks": missing.Picks,
		})
		return
	}
	if err != nil {
		slogError(w, err, "import list")
		return
	}
	// The whole page is stale: every connected browser shows the imported
	// List right away.
	s.changed(RegionTable, RegionHistory, RegionLastPick, RegionFilter, RegionTagPopover, RegionListName)
	writeJSON(w, http.StatusCreated, map[string]any{
		"list_name": res.ListName,
		"entries":   res.Entries,
		"picks":     res.Picks,
	})
}

// isTrue reports whether a request option flag is switched on.
func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// readUpload returns the import file's bytes and its content-type hint. A
// multipart upload carries no usable hint, so its format is sniffed.
func readUpload(r *http.Request) ([]byte, string, error) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxUpload))
		if err != nil {
			return nil, "", errors.New("could not read body")
		}
		return body, ct, nil
	}
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return nil, "", errors.New("could not parse upload: " + err.Error())
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, "", errors.New("missing file field")
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxUpload))
	if err != nil {
		return nil, "", errors.New("could not read upload")
	}
	return body, "", nil
}

func (s *Server) getListName(w http.ResponseWriter, r *http.Request) {
	name, err := s.store.GetListName(r.Context())
	if err != nil {
		slogError(w, err, "get list name")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (s *Server) setListName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Entries"
	}
	if err := s.store.SetListName(r.Context(), name); err != nil {
		slogError(w, err, "set list name")
		return
	}
	s.changed(RegionListName)
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}
