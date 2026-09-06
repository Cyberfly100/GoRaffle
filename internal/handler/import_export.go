package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/goraffle/raffle/internal/model"
)

// importRow is one entry in an import payload.
type importRow struct {
	Name      string   `json:"name"`
	PickCount *int     `json:"pick_count,omitempty"`
	Excluded  *bool    `json:"excluded,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

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

// writeCSVAttachment streams a CSV file as a download.
func writeCSVAttachment(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	cw := csv.NewWriter(w)
	if len(header) > 0 {
		_ = cw.Write(header)
	}
	for _, row := range rows {
		_ = cw.Write(row)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slogErrorLogger(err, "write csv "+filename)
	}
}

func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	format := queryFormat(r, "json")

	// Get list name from query param (for dynamic export links) or from DB
	listName := r.URL.Query().Get("name")
	if listName == "" {
		var err error
		listName, err = s.store.GetListName(r.Context())
		if err != nil {
			slogError(w, err, "get list name for export")
			return
		}
	}

	cs, err := s.store.ListEntries(r.Context())
	if err != nil {
		slogError(w, err, "list entries for export")
		return
	}

	rows := make([]importRow, 0, len(cs))
	for _, c := range cs {
		rows = append(rows, importRow{
			Name:      c.Name,
			PickCount: &c.PickCount,
			Excluded:  &c.Excluded,
			Tags:      c.Tags,
		})
	}

	safeName := sanitizeFilename(listName)
	if safeName == "" {
		safeName = "raffle"
	}

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeName+`.json"`)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"list_name": listName,
			"entries":   rows,
		})
	case "csv":
		csvRows := make([][]string, 0, len(rows)+2)
		// First row: list_name header
		csvRows = append(csvRows, []string{"list_name", listName})
		// Second row: column headers
		csvRows = append(csvRows, []string{"name", "pick_count", "excluded", "tags"})
		for _, row := range rows {
			tags := strings.Join(row.Tags, "|")
			excluded := "false"
			count := "0"
			if row.Excluded != nil {
				excluded = strconv.FormatBool(*row.Excluded)
			}
			if row.PickCount != nil {
				count = strconv.Itoa(*row.PickCount)
			}
			csvRows = append(csvRows, []string{row.Name, count, excluded, tags})
		}
		writeCSVAttachment(w, safeName+".csv", nil, csvRows)
	default:
		writeErr(w, http.StatusBadRequest, "format must be json or csv")
	}
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
		writeCSVAttachment(w, "raffle-history.csv", []string{"draw_id", "pick_number", "entry_id", "entry_name", "picked_at"}, rows)
	default:
		writeErr(w, http.StatusBadRequest, "format must be json or csv")
	}
}

func (s *Server) importData(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")

	var body []byte
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not parse upload: "+err.Error())
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "missing file field")
			return
		}
		defer file.Close()
		body, err = io.ReadAll(io.LimitReader(file, 10<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not read upload")
			return
		}
	default:
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not read body")
			return
		}
	}

	var rows []importRow
	var listName string
	var err error
	switch {
	case strings.Contains(ct, "text/csv"):
		rows, listName, err = parseCSV(body)
	case strings.Contains(ct, "application/json"):
		rows, listName, err = parseJSONImport(body)
	default:
		// Multipart or untyped: sniff by first non-whitespace char.
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
			rows, listName, err = parseJSONImport(body)
		} else {
			rows, listName, err = parseCSV(body)
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not parse import: "+err.Error())
		return
	}

	if listName != "" {
		if err := s.store.SetListName(r.Context(), listName); err != nil {
			slogErrorLogger(err, "set list name on import")
		}
	}

	added, updated, err := s.store.ImportEntries(r.Context(), rowsToImport(rows))
	if err != nil {
		slogError(w, err, "import entries")
		return
	}
	s.broadcast(EventEntries, map[string]int{"added": added, "updated": updated})
	writeJSON(w, http.StatusCreated, map[string]any{"added": added, "updated": updated, "list_name": listName})
}

func parseJSONImport(body []byte) ([]importRow, string, error) {
	var payload struct {
		ListName string      `json:"list_name"`
		Entries  []importRow `json:"entries"`
		Legacy   []importRow `json:"contestants"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		rows := append(payload.Entries, payload.Legacy...)
		if len(rows) == 0 {
			return nil, payload.ListName, fmt.Errorf("JSON contained no entries under \"entries\" (or legacy \"contestants\")")
		}
		return rows, payload.ListName, nil
	}
	var names []string
	if err := json.Unmarshal(body, &names); err == nil {
		var rows []importRow
		for _, n := range names {
			rows = append(rows, importRow{Name: n})
		}
		if len(rows) == 0 {
			return nil, "", fmt.Errorf("JSON contained no entry names")
		}
		return rows, "", nil
	}
	var single []importRow
	if err := json.Unmarshal(body, &single); err == nil {
		if len(single) == 0 {
			return nil, "", fmt.Errorf("JSON contained no entries")
		}
		return single, "", nil
	}
	return nil, "", fmt.Errorf("invalid JSON: expected {list_name:string, entries:[...]} (or legacy {contestants:[...]}), [\"a\",\"b\"] or [{...}]")
}

func rowsToImport(rows []importRow) []model.EntryImport {
	out := make([]model.EntryImport, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.EntryImport{
			Name:      strings.TrimSpace(r.Name),
			PickCount: r.PickCount,
			Excluded:  r.Excluded,
			Tags:      r.Tags,
		})
	}
	return out
}

func parseCSV(body []byte) ([]importRow, string, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	// The list_name metadata row is narrower than the header/data rows, so
	// records legitimately vary in width.
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, "", err
	}
	if len(records) == 0 {
		return nil, "", fmt.Errorf("empty csv")
	}

	listName := ""
	startIdx := 0
	// Check if first row is list_name header
	if len(records) > 0 && strings.EqualFold(records[0][0], "list_name") {
		if len(records[0]) > 1 {
			listName = strings.TrimSpace(records[0][1])
		}
		startIdx = 1
	}

	var rows []importRow
	for i := startIdx; i < len(records); i++ {
		record := records[i]
		if i == startIdx && strings.EqualFold(record[0], "name") {
			continue // header
		}
		if len(record) < 1 || strings.TrimSpace(record[0]) == "" {
			continue
		}
		row := importRow{Name: strings.TrimSpace(record[0])}
		if len(record) > 1 && strings.TrimSpace(record[1]) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(record[1])); err == nil {
				row.PickCount = &n
			}
		}
		if len(record) > 2 {
			if b, err := strconv.ParseBool(strings.TrimSpace(record[2])); err == nil {
				row.Excluded = &b
			}
		}
		if len(record) > 3 {
			for _, t := range strings.Split(record[3], "|") {
				t = strings.TrimSpace(t)
				if t != "" {
					row.Tags = append(row.Tags, t)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, listName, nil
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
