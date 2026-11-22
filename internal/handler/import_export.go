package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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

func (s *Server) exportHistory(w http.ResponseWriter, r *http.Request) {
	picks, err := s.store.ListHistory(r.Context())
	if err != nil {
		slogError(w, err, "list history for export")
		return
	}

	format := queryFormat(r, "csv")
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="raffle-history.json"`)
		_ = json.NewEncoder(w).Encode(picks)
	case "csv":
		rows := make([][]string, 0, len(picks))
		for _, p := range picks {
			// Same order as the on-screen history: most recent draw first.
			rows = append(rows, []string{
				strconv.Itoa(p.ID),
				strconv.Itoa(p.PickNumber),
				strconv.Itoa(p.EntryID),
				p.EntryName,
				p.PickedAt.UTC().Format(time.RFC3339),
			})
		}
		body, err := listexchange.WriteCSV([]string{"draw_id", "pick_number", "entry_id", "entry_name", "picked_at"}, rows)
		if err != nil {
			slogError(w, err, "encode history csv")
			return
		}
		writeAttachment(w, listexchange.CSV.ContentType(), "raffle-history.csv", body)
	default:
		writeErr(w, http.StatusBadRequest, "format must be json or csv")
	}
}

// importData reads an uploaded (multipart "file" field) or directly posted
// List file, decodes it and applies it to the store in one transaction.
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
	added, updated, err := s.store.ApplyList(r.Context(), list)
	if err != nil {
		slogError(w, err, "import list")
		return
	}
	s.broadcast(EventEntries, map[string]int{"added": added, "updated": updated})
	writeJSON(w, http.StatusCreated, map[string]any{"added": added, "updated": updated, "list_name": list.Name})
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
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}
