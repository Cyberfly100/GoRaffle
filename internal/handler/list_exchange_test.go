package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// listEntry is one Entry as the entries endpoint reports it, reduced to the
// fields a List carries.
type listEntry struct {
	Name      string   `json:"name"`
	PickCount int      `json:"pick_count"`
	Excluded  bool     `json:"excluded"`
	Tags      []string `json:"tags"`
}

// listSnapshot is the List as seen through the List name and entries endpoints.
type listSnapshot struct {
	Name    string
	Entries []listEntry
}

func readList(t *testing.T, ts *httptest.Server) listSnapshot {
	t.Helper()
	var name struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(bodyOf(t, ts, "/api/list-name"), &name); err != nil {
		t.Fatalf("list name: %v", err)
	}
	var entries []listEntry
	if err := json.Unmarshal(bodyOf(t, ts, "/api/entries"), &entries); err != nil {
		t.Fatalf("entries: %v", err)
	}
	for i := range entries {
		if entries[i].Tags == nil {
			entries[i].Tags = []string{}
		}
	}
	return listSnapshot{Name: name.Name, Entries: entries}
}

// pickJSON is one Pick as the history endpoint reports it.
type pickJSON struct {
	EntryName  string `json:"entry_name"`
	PickNumber int    `json:"pick_number"`
	PickedAt   string `json:"picked_at"`
}

// readHistory returns the History oldest first (the order it was picked
// in), as the history endpoint reports it (the endpoint lists newest first).
func readHistory(t *testing.T, ts *httptest.Server) []pickJSON {
	t.Helper()
	var picks []pickJSON
	if err := json.Unmarshal(bodyOf(t, ts, "/api/history"), &picks); err != nil {
		t.Fatalf("history: %v", err)
	}
	for i, j := 0, len(picks)-1; i < j; i, j = i+1, j-1 {
		picks[i], picks[j] = picks[j], picks[i]
	}
	return picks
}

// seedList builds a List through the entry, tag and List name endpoints.
func seedList(t *testing.T, ts *httptest.Server, listName string, entries []listEntry) {
	t.Helper()
	putJSON(t, ts, "/api/list-name", jsonString(t, map[string]string{"name": listName}))
	for _, e := range entries {
		var created struct {
			ID int `json:"id"`
		}
		res := postJSON(t, ts, "/api/entries", jsonString(t, map[string]string{"name": e.Name}))
		if err := json.Unmarshal(res, &created); err != nil {
			t.Fatalf("add entry: %v (%s)", err, res)
		}
		putJSON(t, ts, "/api/entries/"+itoa(created.ID), jsonString(t, map[string]any{
			"pick_count": e.PickCount,
			"excluded":   e.Excluded,
		}))
		for _, tag := range e.Tags {
			postJSON(t, ts, "/api/entries/"+itoa(created.ID)+"/tags", jsonString(t, map[string]string{"name": tag}))
		}
	}
}

// clearList removes every Entry and resets the List name, as if the database
// had been rebuilt.
func clearList(t *testing.T, ts *httptest.Server) {
	t.Helper()
	reqJSON(t, ts, "DELETE", "/api/entries", "")
	putJSON(t, ts, "/api/list-name", `{"name":"Entries"}`)
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// export downloads the List in the given format and returns the body and the
// download file name.
func export(t *testing.T, ts *httptest.Server, format string) ([]byte, string) {
	t.Helper()
	res := doReq(t, ts, "GET", "/api/export?format="+format, nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("export %s = %d (%s)", format, res.StatusCode, body)
	}
	return body, res.Header.Get("Content-Disposition")
}

// importRaw posts content directly to the import endpoint.
func importRaw(t *testing.T, ts *httptest.Server, contentType string, body []byte) (int, []byte) {
	t.Helper()
	return importRawOpts(t, ts, contentType, body, "")
}

// importRawOpts posts content to the import endpoint with option flags in
// the query string.
func importRawOpts(t *testing.T, ts *httptest.Server, contentType string, body []byte, query string) (int, []byte) {
	t.Helper()
	url := ts.URL + "/api/import"
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, out
}

func mustImport(t *testing.T, ts *httptest.Server, contentType string, body []byte) {
	t.Helper()
	if status, out := importRaw(t, ts, contentType, body); status != http.StatusCreated {
		t.Fatalf("import = %d (%s)", status, out)
	}
}

func assertList(t *testing.T, got, want listSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// A List exercising everything the round trip must preserve. Entries are in
// the order the entries endpoint lists them (by name); Tags are by name.
var richList = listSnapshot{
	Name: "Friday Raffle, \"Q4\"",
	Entries: []listEntry{
		{Name: "Alice", PickCount: 3, Excluded: false, Tags: []string{"TeamA", "VIP"}},
		{Name: "Bob, Jr.", PickCount: 0, Excluded: true, Tags: []string{`back\slash`, "Red|Blue"}},
		{Name: "Carol \"The Hammer\"", PickCount: 7, Excluded: false, Tags: []string{"a,b", "x\"y"}},
		{Name: "Dan\nSecond line", PickCount: 1, Excluded: true, Tags: []string{"TeamA"}},
	},
}

// The List file carries the complete History: one JSON Pick per Draw, ordered
// by Pick number, even when the History is empty.
func TestExportCarriesHistory(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Drawn List", []listEntry{
		{Name: "Alice", Tags: []string{"VIP"}},
		{Name: "Bob", Tags: []string{}},
	})

	// With no Picks yet the file still carries the (empty) History.
	first, _ := export(t, ts, "json")
	var empty struct {
		Picks []json.RawMessage `json:"picks"`
	}
	if err := json.Unmarshal(first, &empty); err != nil {
		t.Fatalf("export without history: %v (%s)", err, first)
	}
	if empty.Picks == nil || len(empty.Picks) != 0 {
		t.Errorf("export picks = %v, want an empty non-nil array", empty.Picks)
	}

	// Two Draws empty the Pot: the winners are Alice and Bob, in the order
	// the Draw responses name them.
	var order []string
	for i := 0; i < 2; i++ {
		var res struct {
			Winner struct {
				Name string `json:"name"`
			} `json:"winner"`
		}
		if err := json.Unmarshal(postJSON(t, ts, "/api/draw", `{"must_have":[],"any_of":[]}`), &res); err != nil {
			t.Fatalf("draw response: %v", err)
		}
		order = append(order, res.Winner.Name)
	}

	body, _ := export(t, ts, "json")
	var file struct {
		ListName string `json:"list_name"`
		Picks    []struct {
			EntryName  string `json:"entry_name"`
			PickNumber int    `json:"pick_number"`
			PickedAt   string `json:"picked_at"`
		} `json:"picks"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatalf("export: %v (%s)", err, body)
	}
	if file.ListName != "Drawn List" {
		t.Errorf("export list_name = %q, want %q", file.ListName, "Drawn List")
	}
	if len(file.Picks) != 2 {
		t.Fatalf("export picks = %d, want 2", len(file.Picks))
	}
	for i, p := range file.Picks {
		if p.PickNumber != i+1 {
			t.Errorf("picks[%d].pick_number = %d, want %d", i, p.PickNumber, i+1)
		}
		if p.EntryName != order[i] {
			t.Errorf("picks[%d].entry_name = %q, want %q", i, p.EntryName, order[i])
		}
		if _, err := time.Parse(time.RFC3339, p.PickedAt); err != nil {
			t.Errorf("picks[%d].picked_at = %q, not RFC3339: %v", i, p.PickedAt, err)
		}
	}
}

func TestListRoundTripJSON(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, richList.Name, richList.Entries)
	assertList(t, readList(t, ts), richList)

	first, _ := export(t, ts, "json")
	clearList(t, ts)
	mustImport(t, ts, "application/json", first)
	assertList(t, readList(t, ts), richList)

	second, _ := export(t, ts, "json")
	if !bytes.Equal(first, second) {
		t.Errorf("re-export differs\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestListRoundTripMultipartUpload(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, richList.Name, richList.Entries)

	// The dialog uploads without a content-type hint, so the format is
	// sniffed from the bytes.
	exported, _ := export(t, ts, "json")
	clearList(t, ts)
	uploadFile(t, ts, "list.json", exported)
	assertList(t, readList(t, ts), richList)
}

// uploadFile posts a file through the import dialog's multipart form.
func uploadFile(t *testing.T, ts *httptest.Server, filename string, content []byte) {
	t.Helper()
	var buf bytes.Buffer
	boundary := "goraffle-test-boundary"
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString(`Content-Disposition: form-data; name="file"; filename="` + filename + "\"\r\n")
	buf.WriteString("Content-Type: application/octet-stream\r\n\r\n")
	buf.Write(content)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	mustImport(t, ts, "multipart/form-data; boundary="+boundary, buf.Bytes())
}

func TestExportFileNameFollowsListName(t *testing.T) {
	ts := newTestServer(t)
	putJSON(t, ts, "/api/list-name", `{"name":"Team Lunch"}`)
	_, disposition := export(t, ts, "json")
	if !strings.Contains(disposition, `filename="Team_Lunch.json"`) {
		t.Errorf("export Content-Disposition = %q, want filename=\"Team_Lunch.json\"", disposition)
	}
}

// The List exports as one JSON List file. The CSV List export and the
// separate History export are gone: requests for them fail, and the
// interface offers exactly one export.
func TestRemovedExportsAreGone(t *testing.T) {
	ts := newTestServer(t)

	res := doReq(t, ts, "GET", "/api/export?format=csv", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("csv list export = %d, want 400", res.StatusCode)
	}
	for _, format := range []string{"csv", "json"} {
		res := doReq(t, ts, "GET", "/api/export/history?format="+format, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("history export %s = %d, want 404", format, res.StatusCode)
		}
	}

	page := bodyOf(t, ts, "/")
	exports := strings.Count(string(page), "data-export-format")
	if exports != 1 {
		t.Errorf("page offers %d exports, want exactly one (the List file):\n%s", exports, page)
	}
	if !strings.Contains(string(page), `data-export-format="json"`) {
		t.Errorf("page does not offer the List file (JSON):\n%s", page)
	}
	history := bodyOf(t, ts, "/partials/history")
	if strings.Contains(string(history), "/api/export") {
		t.Errorf("history partial still offers an export:\n%s", history)
	}
}

// An import that fails in the database partway through changes nothing: not
// the Entries before the failing one, not the List name, and not the History
// the Import was about to replace.
func TestImportFailingInDatabaseChangesNothing(t *testing.T) {
	ts := newTestServer(t)
	before := listSnapshot{
		Name:    "Keep Me",
		Entries: []listEntry{{Name: "Existing", PickCount: 2, Excluded: false, Tags: []string{"Old"}}},
	}
	seedList(t, ts, before.Name, before.Entries)
	postJSON(t, ts, "/api/draw", `{}`) // Existing wins: the History is non-empty
	before.Entries[0].PickCount = 3    // the Draw added one

	// The last Entry's Pick count overflows the database column, so the
	// import fails after the List name and the earlier Entries were written.
	bad := `{"list_name":"Renamed","entries":[
		{"name":"Existing","pick_count":9,"tags":["New"]},
		{"name":"Fresh"},
		{"name":"Overflow","pick_count":3000000000}
	]}`
	if status, out := importRaw(t, ts, "application/json", []byte(bad)); status < 400 {
		t.Fatalf("failing import = %d (%s), want an error", status, out)
	}
	assertList(t, readList(t, ts), before)
	if picks := readHistory(t, ts); len(picks) != 1 || picks[0].EntryName != "Existing" {
		t.Errorf("history after failed import = %+v, want the seeded Pick", picks)
	}
}

func TestImportSupportedShapes(t *testing.T) {
	cases := map[string]struct {
		contentType string
		body        string
		want        listSnapshot
	}{
		"older JSON export without picks": {
			"application/json",
			`{"list_name":"Old Backup","entries":[{"name":"Ann","pick_count":3,"excluded":false,"tags":["VIP"]}]}`,
			listSnapshot{Name: "Old Backup", Entries: []listEntry{{Name: "Ann", PickCount: 3, Tags: []string{"VIP"}}}},
		},
		"legacy contestants key": {
			"application/json",
			`{"list_name":"Old Backup","contestants":[{"name":"Zed","pick_count":4,"excluded":true,"tags":["Alumni"]}]}`,
			listSnapshot{Name: "Old Backup", Entries: []listEntry{{Name: "Zed", PickCount: 4, Excluded: true, Tags: []string{"Alumni"}}}},
		},
		"bare JSON names, sniffed": {
			"",
			`  ["Yan", " Xia "]`,
			listSnapshot{Name: "Entries", Entries: []listEntry{{Name: "Xia", Tags: []string{}}, {Name: "Yan", Tags: []string{}}}},
		},
		"JSON array of entry objects": {
			"application/json",
			`[{"name":"Wes","pick_count":1}]`,
			listSnapshot{Name: "Entries", Entries: []listEntry{{Name: "Wes", PickCount: 1, Tags: []string{}}}},
		},
		"CSV with name row, sniffed": {
			"",
			"list_name,Sheet\nname,pick_count,excluded,tags\nVic,2,true,A|B\n",
			listSnapshot{Name: "Sheet", Entries: []listEntry{{Name: "Vic", PickCount: 2, Excluded: true, Tags: []string{"A", "B"}}}},
		},
		"hand-made CSV of names only": {
			"text/csv",
			"Uma\nTed\n",
			listSnapshot{Name: "Entries", Entries: []listEntry{{Name: "Ted", Tags: []string{}}, {Name: "Uma", Tags: []string{}}}},
		},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			clearList(t, ts)
			mustImport(t, ts, c.contentType, []byte(c.body))
			assertList(t, readList(t, ts), c.want)
		})
	}
}

// An Import replaces, never merges: importing a bare list of names over a
// richer List leaves exactly those names, with no History and no leftover
// fields. The result depends only on the file.
func TestImportReplacesEverything(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Merge", []listEntry{
		{Name: "Ann", PickCount: 5, Excluded: true, Tags: []string{"Keep"}},
		{Name: "Ben", PickCount: 1, Tags: []string{"Old"}},
	})
	postJSON(t, ts, "/api/draw", `{}`) // the History is non-empty
	mustImport(t, ts, "application/json", []byte(`["Ann"]`))
	assertList(t, readList(t, ts), listSnapshot{
		Name:    "Entries",
		Entries: []listEntry{{Name: "Ann", Tags: []string{}}},
	})
	if picks := readHistory(t, ts); len(picks) != 0 {
		t.Errorf("history after import = %+v, want empty", picks)
	}
}

// The History is part of the List file: a Pick with a missing or
// unparseable time makes the whole file invalid, and nothing changes —
// not the Entries, not the List name, not the History.
func TestImportRejectsPickWithInvalidTime(t *testing.T) {
	cases := map[string]string{
		"missing picked_at": `{"list_name":"Bad","entries":[{"name":"Ann"}],"picks":[
			{"entry_name":"Ann","pick_number":1}]}`,
		"unparseable picked_at": `{"list_name":"Bad","entries":[{"name":"Ann"}],"picks":[
			{"entry_name":"Ann","pick_number":1,"picked_at":"not a time"}]}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			seedList(t, ts, "Untouched", []listEntry{{Name: "Ann", PickCount: 1, Tags: []string{}}})
			postJSON(t, ts, "/api/draw", `{}`) // Ann wins: the History is non-empty
			before := readList(t, ts)

			status, out := importRaw(t, ts, "application/json", []byte(body))
			if status != http.StatusBadRequest {
				t.Fatalf("import = %d (%s), want 400", status, out)
			}
			if !strings.Contains(string(out), "picked_at") {
				t.Errorf("import error = %s, want it to name picked_at", out)
			}
			assertList(t, readList(t, ts), before)
			if picks := readHistory(t, ts); len(picks) != 1 || picks[0].EntryName != "Ann" {
				t.Errorf("history after rejected import = %+v, want the seeded Pick", picks)
			}
		})
	}
}

// A file naming the same Entry twice (case-insensitively) is rejected with
// the duplicated names listed, and nothing changes.
func TestImportRejectsDuplicateEntryNames(t *testing.T) {
	cases := map[string]struct {
		contentType string
		body        string
		wantName    string
	}{
		"same name twice": {
			"application/json",
			`{"entries":[{"name":"Ann"},{"name":"Ann"}]}`,
			"Ann",
		},
		"same name in different case": {
			"application/json",
			`{"entries":[{"name":"Ann"},{"name":"ann"}]}`,
			"ann",
		},
		"CSV duplicate": {
			"text/csv",
			"Ann\nann\n",
			"ann",
		},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			seedList(t, ts, "Untouched", []listEntry{{Name: "Ann", PickCount: 1, Tags: []string{}}})
			postJSON(t, ts, "/api/draw", `{}`) // Ann wins: the History is non-empty
			before := readList(t, ts)

			status, out := importRaw(t, ts, c.contentType, []byte(c.body))
			if status != http.StatusBadRequest {
				t.Fatalf("import = %d (%s), want 400", status, out)
			}
			if !strings.Contains(string(out), "duplicate") || !strings.Contains(string(out), c.wantName) {
				t.Errorf("import error = %s, want it to list the duplicated name %q", out, c.wantName)
			}
			assertList(t, readList(t, ts), before)
			if picks := readHistory(t, ts); len(picks) != 1 || picks[0].EntryName != "Ann" {
				t.Errorf("history after rejected import = %+v, want the seeded Pick", picks)
			}
		})
	}
}

// Importing a List file restores exactly what the file says: the List
// name, the Entries and the History are replaced, Entries absent from the
// file are gone, and exporting again reproduces the file byte for byte.
func TestImportRestoresTheListFile(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Drawn List", []listEntry{
		{Name: "Ann", Tags: []string{"VIP"}},
		{Name: "Ben", Tags: []string{}},
	})
	// Two Draws pick Ann and Ben once each, in an order only the file knows.
	postJSON(t, ts, "/api/draw", `{}`)
	postJSON(t, ts, "/api/draw", `{}`)
	drawnPicks := readHistory(t, ts)
	if len(drawnPicks) != 2 {
		t.Fatalf("seeded history has %d Picks, want 2", len(drawnPicks))
	}

	first, _ := export(t, ts, "json")
	var file struct {
		Picks []pickJSON `json:"picks"`
	}
	if err := json.Unmarshal(first, &file); err != nil {
		t.Fatalf("export: %v (%s)", err, first)
	}

	// A different List is in place: the file must fully replace it.
	seedList(t, ts, "Other List", []listEntry{
		{Name: "Zed", PickCount: 9, Excluded: true, Tags: []string{"Old"}},
	})
	mustImport(t, ts, "application/json", first)

	want := listSnapshot{
		Name: "Drawn List",
		Entries: []listEntry{
			{Name: "Ann", PickCount: 1, Tags: []string{"VIP"}},
			{Name: "Ben", PickCount: 1, Tags: []string{}},
		},
	}
	assertList(t, readList(t, ts), want)
	if picks := readHistory(t, ts); len(picks) != 2 {
		t.Errorf("history after import = %d Picks, want the file's 2", len(picks))
	} else {
		for i, p := range picks {
			if p.EntryName != file.Picks[i].EntryName || p.PickNumber != i+1 {
				t.Errorf("pick %d after import = %+v, want %s #%d", i, p, file.Picks[i].EntryName, i+1)
			}
			if p.PickedAt != file.Picks[i].PickedAt {
				t.Errorf("pick %d time after import = %q, want the file's %q", i, p.PickedAt, file.Picks[i].PickedAt)
			}
		}
	}

	second, _ := export(t, ts, "json")
	if !bytes.Equal(first, second) {
		t.Errorf("re-export differs\nfirst:  %s\nsecond: %s", first, second)
	}
}

// A List file without a name resets the List name to "Entries": the
// result depends only on the file.
func TestImportWithoutListNameResetsToEntries(t *testing.T) {
	cases := map[string]struct {
		contentType string
		body        string
	}{
		"bare JSON names":          {"application/json", `["Ann","Ben"]`},
		"JSON object without name": {"application/json", `{"entries":[{"name":"Ann"},{"name":"Ben"}]}`},
		"CSV without name row":     {"text/csv", "name,pick_count,excluded,tags\nAnn,0,false,\nBen,0,false,\n"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			putJSON(t, ts, "/api/list-name", `{"name":"Our List"}`)
			mustImport(t, ts, c.contentType, []byte(c.body))
			assertList(t, readList(t, ts), listSnapshot{
				Name: "Entries",
				Entries: []listEntry{
					{Name: "Ann", Tags: []string{}},
					{Name: "Ben", Tags: []string{}},
				},
			})
		})
	}
}

// A Pick naming an Entry the file doesn't contain stops the Import: the
// response is a distinct error listing the missing names and the number of
// affected Picks, and nothing changes. The same file with the "skip missing
// Picks" option imports and drops those Picks.
func TestImportMissingNames(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Untouched", []listEntry{{Name: "Ann", PickCount: 1, Tags: []string{}}})
	postJSON(t, ts, "/api/draw", `{}`) // Ann wins: the History is non-empty
	before := readList(t, ts)

	file := `{"list_name":"New","entries":[{"name":"Ben"}],"picks":[
		{"entry_name":"Ben","pick_number":1,"picked_at":"2026-11-23T10:00:00Z"},
		{"entry_name":"Ghost","pick_number":2,"picked_at":"2026-11-23T11:00:00Z"},
		{"entry_name":"ghost","pick_number":3,"picked_at":"2026-11-23T12:00:00Z"}]}`

	status, out := importRaw(t, ts, "application/json", []byte(file))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("import = %d (%s), want 422", status, out)
	}
	var e struct {
		Error        string   `json:"error"`
		MissingNames []string `json:"missing_names"`
		MissingPicks int      `json:"missing_picks"`
	}
	if err := json.Unmarshal(out, &e); err != nil {
		t.Fatalf("error response: %v (%s)", err, out)
	}
	if len(e.MissingNames) != 1 || e.MissingNames[0] != "Ghost" {
		t.Errorf("missing_names = %v, want [Ghost] (deduped case-insensitively)", e.MissingNames)
	}
	if e.MissingPicks != 2 {
		t.Errorf("missing_picks = %d, want 2", e.MissingPicks)
	}
	if !strings.Contains(e.Error, "Ghost") || !strings.Contains(e.Error, "2") {
		t.Errorf("error = %q, want it to name Ghost and its 2 Picks", e.Error)
	}
	assertList(t, readList(t, ts), before)
	if picks := readHistory(t, ts); len(picks) != 1 || picks[0].EntryName != "Ann" {
		t.Errorf("history after refused import = %+v, want the seeded Pick", picks)
	}

	// With the option the same file imports without those Picks.
	status, out = importRawOpts(t, ts, "application/json", []byte(file), "skip_missing_picks=1")
	if status != http.StatusCreated {
		t.Fatalf("import with skip_missing_picks = %d (%s), want 201", status, out)
	}
	assertList(t, readList(t, ts), listSnapshot{
		Name:    "New",
		Entries: []listEntry{{Name: "Ben", PickCount: 1, Tags: []string{}}},
	})
	if picks := readHistory(t, ts); len(picks) != 1 || picks[0].EntryName != "Ben" || picks[0].PickNumber != 1 {
		t.Errorf("history after skipped import = %+v, want Ben's Pick #1", picks)
	}
}

// The browser dialog re-sends the file with the option as a form field.
func TestImportSkipMissingPicksAsFormField(t *testing.T) {
	ts := newTestServer(t)
	var buf bytes.Buffer
	boundary := "goraffle-test-boundary"
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Disposition: form-data; name=\"skip_missing_picks\"\r\n\r\n1\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString(`Content-Disposition: form-data; name="file"; filename="list.json"` + "\r\n")
	buf.WriteString("Content-Type: application/octet-stream\r\n\r\n")
	buf.WriteString(`{"entries":[{"name":"Ann"}],"picks":[{"entry_name":"Ghost","pick_number":1,"picked_at":"2026-11-23T10:00:00Z"}]}`)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	status, out := importRaw(t, ts, "multipart/form-data; boundary="+boundary, buf.Bytes())
	if status != http.StatusCreated {
		t.Fatalf("import = %d (%s), want 201", status, out)
	}
	assertList(t, readList(t, ts), listSnapshot{
		Name:    "Entries",
		Entries: []listEntry{{Name: "Ann", Tags: []string{}}},
	})
	if picks := readHistory(t, ts); len(picks) != 0 {
		t.Errorf("history = %+v, want empty", picks)
	}
}

// Picks are sorted by the file's Pick number, ties broken by time, then
// renumbered 1 to n: gaps, duplicate numbers and ties can't survive. They
// match Entries by name case-insensitively, and a Pick count supplied by the
// file wins while an Entry with no count gets the number of its Picks.
func TestImportRenumbersAndCountsPicks(t *testing.T) {
	ts := newTestServer(t)
	clearList(t, ts)
	// Pick numbers have a gap, a tie (broken by time) and a duplicate
	// Entry name in different case; Ann's count is supplied, Ben's isn't.
	file := `{"list_name":"Renum","entries":[{"name":"Ann","pick_count":5},{"name":"Ben"}],"picks":[
		{"entry_name":"ben","pick_number":5,"picked_at":"2026-11-23T03:00:00Z"},
		{"entry_name":"ANN","pick_number":2,"picked_at":"2026-11-23T01:00:00Z"},
		{"entry_name":"ben","pick_number":2,"picked_at":"2026-11-23T02:00:00Z"}]}`
	mustImport(t, ts, "application/json", []byte(file))

	picks := readHistory(t, ts)
	want := []struct {
		name string
		num  int
	}{{"Ann", 1}, {"Ben", 2}, {"Ben", 3}}
	if len(picks) != len(want) {
		t.Fatalf("history = %+v, want %d Picks", picks, len(want))
	}
	for i, p := range picks {
		if p.EntryName != want[i].name || p.PickNumber != want[i].num {
			t.Errorf("pick %d = %s #%d, want %s #%d", i, p.EntryName, p.PickNumber, want[i].name, want[i].num)
		}
	}
	assertList(t, readList(t, ts), listSnapshot{
		Name: "Renum",
		Entries: []listEntry{
			{Name: "Ann", PickCount: 5, Tags: []string{}}, // the file's count wins
			{Name: "Ben", PickCount: 2, Tags: []string{}}, // its Picks in the file
		},
	})
}

// An Entry with no Pick count in the file and no Picks either gets zero.
func TestImportCountsEntriesWithoutPicks(t *testing.T) {
	ts := newTestServer(t)
	mustImport(t, ts, "application/json",
		[]byte(`{"entries":[{"name":"Ann"},{"name":"Cy","pick_count":0}]}`))
	assertList(t, readList(t, ts), listSnapshot{
		Name: "Entries",
		Entries: []listEntry{
			{Name: "Ann", Tags: []string{}},
			{Name: "Cy", Tags: []string{}},
		},
	})
}

// Legacy formats can't carry a History: a CSV (with or without the List
// name row), a bare JSON list of names and an older JSON export all
// replace the List with the file's Entries and an empty History.
func TestLegacyImportsLeaveHistoryEmpty(t *testing.T) {
	cases := map[string]struct {
		contentType string
		body        string
		want        listSnapshot
	}{
		"older JSON export without picks": {
			"application/json",
			`{"list_name":"Old Backup","entries":[{"name":"Ann","pick_count":3,"excluded":false,"tags":["VIP"]}]}`,
			listSnapshot{Name: "Old Backup", Entries: []listEntry{{Name: "Ann", PickCount: 3, Tags: []string{"VIP"}}}},
		},
		"CSV with name row": {
			"text/csv",
			"list_name,Sheet\nname,pick_count,excluded,tags\nVic,2,true,A|B\n",
			listSnapshot{Name: "Sheet", Entries: []listEntry{{Name: "Vic", PickCount: 2, Excluded: true, Tags: []string{"A", "B"}}}},
		},
		"bare JSON names": {
			"application/json",
			`["Ann","Ben"]`,
			listSnapshot{Name: "Entries", Entries: []listEntry{
				{Name: "Ann", Tags: []string{}},
				{Name: "Ben", Tags: []string{}},
			}},
		},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			seedList(t, ts, "Drawn", []listEntry{{Name: "Zed", Tags: []string{}}})
			postJSON(t, ts, "/api/draw", `{}`) // Zed wins: the History is non-empty
			mustImport(t, ts, c.contentType, []byte(c.body))
			assertList(t, readList(t, ts), c.want)
			if picks := readHistory(t, ts); len(picks) != 0 {
				t.Errorf("history after legacy import = %+v, want empty", picks)
			}
		})
	}
}

// readTagNames returns every Tag the tags endpoint lists, by name.
func readTagNames(t *testing.T, ts *httptest.Server) []string {
	t.Helper()
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(bodyOf(t, ts, "/api/tags"), &tags); err != nil {
		t.Fatalf("tags: %v", err)
	}
	names := make([]string, len(tags))
	for i, tg := range tags {
		names[i] = tg.Name
	}
	return names
}

// The "remove unused Tags" option is off by default: Tags no imported Entry
// uses stay available; with the option they are removed.
func TestImportUnusedTagsOption(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Old", []listEntry{
		{Name: "Ann", Tags: []string{"Old"}},
		{Name: "Ben", Tags: []string{"Kept"}},
	})

	// Default: the unused Tag stays.
	mustImport(t, ts, "application/json",
		[]byte(`{"list_name":"New","entries":[{"name":"Cy","tags":["Kept"]}]}`))
	names := readTagNames(t, ts)
	if !slices.Contains(names, "Old") || !slices.Contains(names, "Kept") {
		t.Errorf("tags after import = %v, want Old and Kept: unused Tags stay by default", names)
	}

	// With the option the unused Tag is removed.
	status, out := importRawOpts(t, ts, "application/json",
		[]byte(`{"list_name":"New","entries":[{"name":"Cy","tags":["Kept"]}]}`),
		"remove_unused_tags=1")
	if status != http.StatusCreated {
		t.Fatalf("import with remove_unused_tags = %d (%s), want 201", status, out)
	}
	names = readTagNames(t, ts)
	if slices.Contains(names, "Old") {
		t.Errorf("tags after import with the option = %v, want Old gone", names)
	}
	if !slices.Contains(names, "Kept") {
		t.Errorf("tags after import with the option = %v, want Kept to stay", names)
	}
}

func TestImportRejectsEmptyAndMalformedFiles(t *testing.T) {
	cases := map[string]struct {
		contentType string
		body        string
		wantError   string
	}{
		"empty body":            {"", "", "empty"},
		"whitespace only":       {"text/csv", " \n\t\n", "empty"},
		"malformed JSON":        {"application/json", `{"entries":[{"name":`, "invalid JSON"},
		"malformed CSV":         {"text/csv", "name\n\"unterminated\n", "invalid CSV"},
		"CSV header only":       {"text/csv", "list_name,X\nname,pick_count,excluded,tags\n", "no entries"},
		"JSON object, no entry": {"application/json", `{"list_name":"X"}`, "no entries"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			ts := newTestServer(t)
			before := listSnapshot{Name: "Untouched", Entries: []listEntry{{Name: "Ann", PickCount: 1, Tags: []string{}}}}
			seedList(t, ts, before.Name, before.Entries)
			status, out := importRaw(t, ts, c.contentType, []byte(c.body))
			if status != http.StatusBadRequest {
				t.Errorf("import = %d (%s), want 400", status, out)
			}
			if !strings.Contains(string(out), c.wantError) {
				t.Errorf("import error = %s, want it to mention %q", out, c.wantError)
			}
			assertList(t, readList(t, ts), before)
		})
	}
}
