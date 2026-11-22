package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
	req, err := http.NewRequest("POST", ts.URL+"/api/import", bytes.NewReader(body))
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

func TestListRoundTripCSV(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, richList.Name, richList.Entries)

	first, _ := export(t, ts, "csv")
	clearList(t, ts)
	mustImport(t, ts, "text/csv", first)
	assertList(t, readList(t, ts), richList)

	second, _ := export(t, ts, "csv")
	if !bytes.Equal(first, second) {
		t.Errorf("re-export differs\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestListRoundTripMultipartUpload(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, richList.Name, richList.Entries)

	for _, format := range []string{"json", "csv"} {
		exported, _ := export(t, ts, format)
		clearList(t, ts)
		uploadFile(t, ts, "list."+format, exported)
		assertList(t, readList(t, ts), richList)
	}
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
	for format, want := range map[string]string{"json": `filename="Team_Lunch.json"`, "csv": `filename="Team_Lunch.csv"`} {
		_, disposition := export(t, ts, format)
		if !strings.Contains(disposition, want) {
			t.Errorf("export %s Content-Disposition = %q, want %s", format, disposition, want)
		}
	}
}
