// Package listexchange owns the List file: the one JSON format a List is
// exported as, and every shape GoRaffle has ever imported.
//
// It is pure: Decode turns uploaded bytes into a model.List and Encode turns a
// model.List into bytes. Applying a decoded List to the database is the store's
// job; reading the upload and writing the response is the handlers' job.
//
// Supported import shapes:
//
//   - JSON object: {"list_name": "...", "entries": [{name, pick_count, excluded, tags}],
//     "picks": [{entry_name, pick_number, picked_at}]} where picked_at is a
//     RFC 3339 time. A Pick with a missing or unparseable time makes the
//     whole file invalid.
//   - JSON object with the legacy "contestants" key instead of "entries"
//   - JSON array of names: ["a", "b"]
//   - JSON array of entry objects: [{"name": "a"}, ...]
//   - CSV with an optional first row "list_name,<name>", an optional header
//     row "name,pick_count,excluded,tags", then one row per Entry. Tags are
//     separated by "|"; a literal "|" or "\" inside a Tag is escaped with "\".
//     CSV is a legacy import format: it cannot carry the History.
package listexchange

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goraffle/raffle/internal/model"
)

// Format is an export file format. The List file is JSON; there is no other
// export format.
type Format string

const JSON Format = "json"

// ParseFormat validates a format name such as the export's ?format= value.
// The CSV List export is gone, so it no longer accepts csv.
func ParseFormat(s string) (Format, error) {
	if Format(s) == JSON {
		return JSON, nil
	}
	return "", errors.New("format must be json")
}

// Extension is the file extension (without dot) for the format.
func (f Format) Extension() string { return string(f) }

// ContentType is the MIME type a file in this format is served with.
func (f Format) ContentType() string {
	return "application/json"
}

// Decode turns an uploaded file into a List. contentTypeHint is the upload's
// Content-Type, if any; "text/csv" and "application/json" force that format,
// anything else is sniffed from the content. Names and Tags are trimmed.
// The returned error describes what is wrong with the file.
func Decode(body []byte, contentTypeHint string) (model.List, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return model.List{}, errors.New("file is empty")
	}
	var (
		l   model.List
		err error
	)
	switch {
	case strings.Contains(contentTypeHint, "text/csv"):
		l, err = decodeCSV(body)
	case strings.Contains(contentTypeHint, "application/json"), looksLikeJSON(body):
		l, err = decodeJSON(body)
	default:
		l, err = decodeCSV(body)
	}
	if err != nil {
		return model.List{}, err
	}
	l = normalize(l)
	if dups := duplicatedNames(l.Entries); len(dups) > 0 {
		return model.List{}, fmt.Errorf("duplicate entries in the file: %s", strings.Join(dups, ", "))
	}
	return l, nil
}

// duplicatedNames lists, in the form they appear, every Entry name that
// occurs more than once (case-insensitively).
func duplicatedNames(entries []model.ListEntry) []string {
	seen := make(map[string]bool, len(entries))
	var dups []string
	listed := make(map[string]bool)
	for _, e := range entries {
		key := strings.ToLower(e.Name)
		if !seen[key] {
			seen[key] = true
			continue
		}
		if !listed[key] {
			listed[key] = true
			dups = append(dups, e.Name)
		}
	}
	return dups
}

func looksLikeJSON(body []byte) bool {
	t := bytes.TrimSpace(body)
	return len(t) > 0 && (t[0] == '[' || t[0] == '{')
}

// normalize trims names and Tags and drops nameless Entries and empty Tags.
func normalize(l model.List) model.List {
	l.Name = strings.TrimSpace(l.Name)
	out := make([]model.ListEntry, 0, len(l.Entries))
	for _, e := range l.Entries {
		e.Name = strings.TrimSpace(e.Name)
		if e.Name == "" {
			continue
		}
		var tags []string
		for _, t := range e.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
		e.Tags = tags
		out = append(out, e)
	}
	l.Entries = out
	return l
}

// jsonEntry is one Entry in the JSON format.
type jsonEntry struct {
	Name      string   `json:"name"`
	PickCount *int     `json:"pick_count,omitempty"`
	Excluded  *bool    `json:"excluded,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// jsonPick is one Pick of the History in the JSON format.
type jsonPick struct {
	EntryName  string `json:"entry_name"`
	PickNumber int    `json:"pick_number"`
	PickedAt   string `json:"picked_at"`
}

// jsonList is the JSON export layout. Field order fixes the byte layout.
type jsonList struct {
	Entries  []jsonEntry `json:"entries"`
	ListName string      `json:"list_name"`
	Picks    []jsonPick  `json:"picks"`
}

// jsonImport is every JSON shape the Import has accepted as an object: the
// jsonList export layout plus the legacy "contestants" key. Decoding into it
// keeps the export layout and the decode layout from drifting apart.
type jsonImport struct {
	jsonList
	Legacy []jsonEntry `json:"contestants"`
}

func decodeJSON(body []byte) (model.List, error) {
	var obj jsonImport
	if err := json.Unmarshal(body, &obj); err == nil {
		rows := append(obj.Entries, obj.Legacy...)
		if len(rows) == 0 {
			return model.List{}, errors.New(`JSON contained no entries under "entries" (or legacy "contestants")`)
		}
		picks, err := decodePicks(obj.Picks)
		if err != nil {
			return model.List{}, err
		}
		return model.List{Name: obj.ListName, Entries: fromJSON(rows), Picks: picks}, nil
	}
	var names []string
	if err := json.Unmarshal(body, &names); err == nil {
		if len(names) == 0 {
			return model.List{}, errors.New("JSON contained no entry names")
		}
		entries := make([]model.ListEntry, 0, len(names))
		for _, n := range names {
			entries = append(entries, model.ListEntry{Name: n})
		}
		return model.List{Entries: entries}, nil
	}
	var rows []jsonEntry
	if err := json.Unmarshal(body, &rows); err == nil {
		if len(rows) == 0 {
			return model.List{}, errors.New("JSON contained no entries")
		}
		return model.List{Entries: fromJSON(rows)}, nil
	}
	return model.List{}, errors.New(`invalid JSON: expected {list_name:string, entries:[...]} (or legacy {contestants:[...]}), ["a","b"] or [{...}]`)
}

func fromJSON(rows []jsonEntry) []model.ListEntry {
	out := make([]model.ListEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.ListEntry(r))
	}
	return out
}

// decodePicks turns the JSON Picks into the List's History. Every Pick
// must name its Entry and carry a parseable RFC 3339 time: the History is
// never silently reordered or dropped.
func decodePicks(rows []jsonPick) ([]model.ListPick, error) {
	out := make([]model.ListPick, 0, len(rows))
	for i, r := range rows {
		name := strings.TrimSpace(r.EntryName)
		at := strings.TrimSpace(r.PickedAt)
		if at == "" {
			return nil, fmt.Errorf("pick %d (%q) is missing picked_at", i+1, name)
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, fmt.Errorf("pick %d (%q) has an invalid picked_at %q (want RFC 3339, e.g. 2026-11-23T12:00:00Z)", i+1, name, at)
		}
		out = append(out, model.ListPick{EntryName: name, PickedAt: t, PickNumber: r.PickNumber})
	}
	return out, nil
}

func decodeCSV(body []byte) (model.List, error) {
	r := csv.NewReader(bytes.NewReader(body))
	// The list_name row is narrower than the header and Entry rows.
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return model.List{}, fmt.Errorf("invalid CSV: %w", err)
	}

	var l model.List
	i := 0
	if i < len(records) && strings.EqualFold(strings.TrimSpace(records[i][0]), "list_name") {
		if len(records[i]) > 1 {
			l.Name = records[i][1]
		}
		i++
	}
	if i < len(records) && strings.EqualFold(strings.TrimSpace(records[i][0]), "name") {
		i++ // column header
	}
	for ; i < len(records); i++ {
		rec := records[i]
		e := model.ListEntry{Name: rec[0]}
		if len(rec) > 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(rec[1])); err == nil {
				e.PickCount = &n
			}
		}
		if len(rec) > 2 {
			if b, err := strconv.ParseBool(strings.TrimSpace(rec[2])); err == nil {
				e.Excluded = &b
			}
		}
		if len(rec) > 3 {
			e.Tags = splitTags(rec[3])
		}
		l.Entries = append(l.Entries, e)
	}
	if len(normalize(l).Entries) == 0 {
		return model.List{}, errors.New("CSV contained no entries")
	}
	return l, nil
}

// splitTags splits a CSV Tags cell on unescaped "|", undoing "\" escapes.
func splitTags(cell string) []string {
	var tags []string
	var cur strings.Builder
	escaped := false
	for _, r := range cell {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			tags = append(tags, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if escaped {
		cur.WriteRune('\\')
	}
	return append(tags, cur.String())
}

// Encode turns a List into a List file: JSON, the one export format. Absent
// PickCount and Excluded are written as 0 and false.
func Encode(l model.List, f Format) ([]byte, error) {
	if f != JSON {
		return nil, fmt.Errorf("unknown format %q", f)
	}
	return encodeJSON(l)
}

func encodeJSON(l model.List) ([]byte, error) {
	out := jsonList{
		ListName: l.Name,
		Entries:  make([]jsonEntry, 0, len(l.Entries)),
		// The History is always present, empty or not, so a List file
		// never lacks the Picks key.
		Picks: make([]jsonPick, 0, len(l.Picks)),
	}
	for _, e := range l.Entries {
		out.Entries = append(out.Entries, jsonEntry{
			Name:      e.Name,
			PickCount: intOr(e.PickCount, 0),
			Excluded:  boolOr(e.Excluded, false),
			Tags:      e.Tags,
		})
	}
	for _, p := range l.Picks {
		out.Picks = append(out.Picks, jsonPick{
			EntryName:  p.EntryName,
			PickNumber: p.PickNumber,
			PickedAt:   p.PickedAt.UTC().Format(time.RFC3339),
		})
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func intOr(p *int, def int) *int {
	if p != nil {
		return p
	}
	return &def
}

func boolOr(p *bool, def bool) *bool {
	if p != nil {
		return p
	}
	return &def
}
