package handler_test

import (
	"html"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// tableRow is one Entry row of the table partial as rendered.
type tableRow struct {
	Name     string
	Eligible bool
	InPot    bool
}

var rowRE = regexp.MustCompile(`(?s)<div class="row([^"]*)" data-id="\d+"[^>]*>\s*<textarea[^>]*>([^<]*)</textarea>`)

// tableFor fetches the table partial for a Tag filter and returns its rows.
func tableFor(t *testing.T, ts *httptest.Server, mustHave, anyOf []string) []tableRow {
	t.Helper()
	q := url.Values{"must_have": mustHave, "any_of": anyOf}
	body := string(bodyOf(t, ts, "/partials/table?"+q.Encode()))
	var rows []tableRow
	for _, m := range rowRE.FindAllStringSubmatch(body, -1) {
		classes := strings.Fields(m[1])
		rows = append(rows, tableRow{
			Name:     html.UnescapeString(m[2]),
			Eligible: !contains(classes, "row-ineligible"),
			InPot:    contains(classes, "row-pot"),
		})
	}
	return rows
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// marked returns the sorted names of the rows that pass is.
func marked(rows []tableRow, is func(tableRow) bool) []string {
	out := []string{}
	for _, r := range rows {
		if is(r) {
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out
}

func eligible(r tableRow) bool { return r.Eligible }
func inPot(r tableRow) bool    { return r.InPot }

func TestTableMarksTheEligiblePoolForTheTagFilter(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Team", []listEntry{
		{Name: "Ada", Tags: []string{"backend", "senior"}},
		{Name: "Bob", Tags: []string{"backend"}},
		{Name: "Cy", Tags: []string{"frontend"}},
		{Name: "Dee", Tags: []string{"frontend", "senior"}},
		{Name: "Eve", Tags: []string{"backend", "senior"}, Excluded: true},
		{Name: "Fin"},
	})

	cases := []struct {
		name            string
		mustHave, anyOf []string
		want            []string
	}{
		{"empty filter", nil, nil, []string{"Ada", "Bob", "Cy", "Dee", "Fin"}},
		{"must-have", []string{"backend", "senior"}, nil, []string{"Ada"}},
		{"any-of", nil, []string{"senior", "frontend"}, []string{"Ada", "Cy", "Dee"}},
		{"combined", []string{"senior"}, []string{"frontend"}, []string{"Dee"}},
		{"unknown tag", []string{"nobody"}, nil, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := tableFor(t, ts, c.mustHave, c.anyOf)
			if len(rows) != 6 {
				t.Fatalf("rendered %d rows, want 6", len(rows))
			}
			if got := marked(rows, eligible); !reflect.DeepEqual(got, c.want) {
				t.Errorf("eligible = %v, want %v", got, c.want)
			}
		})
	}
}

var chipRE = regexp.MustCompile(`class="(?:tag-bubble|chip) ([^"]*)"[^>]*data-tag="([^"]*)"`)

// chipColours returns each Tag's colour classes per rendered chip, in order.
func chipColours(t *testing.T, body string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, m := range chipRE.FindAllStringSubmatch(body, -1) {
		colour := ""
		for _, c := range strings.Fields(m[1]) {
			if strings.HasPrefix(c, "tc-") {
				colour = c
			}
		}
		tag := html.UnescapeString(m[2])
		out[tag] = append(out[tag], colour)
	}
	return out
}

func TestEveryTagChipCarriesItsColour(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Team", []listEntry{
		{Name: "Ada", Tags: []string{"backend", "senior"}},
		{Name: "Bob", Tags: []string{"backend", "remote"}},
	})

	q := url.Values{"must_have": {"senior"}, "any_of": {"remote", "retired"}}.Encode()
	chips := chipColours(t, string(bodyOf(t, ts, "/partials/table?"+q)))
	for tag, cs := range chipColours(t, string(bodyOf(t, ts, "/partials/filter?"+q))) {
		chips[tag] = append(chips[tag], cs...)
	}

	// Table chips, picker chips and the selected zone chips; "retired" is on
	// no Entry, so only its zone chip is shown.
	want := map[string]int{"backend": 3, "senior": 3, "remote": 3, "retired": 1}
	for tag, n := range want {
		cs := chips[tag]
		if len(cs) != n {
			t.Errorf("%q: %d chips, want %d", tag, len(cs), n)
			continue
		}
		for _, c := range cs {
			if c == "" || c != cs[0] {
				t.Errorf("%q chip colours = %v, want one colour on every chip", tag, cs)
				break
			}
		}
	}
}

func TestTableMarksThePotOfTheEligiblePool(t *testing.T) {
	ts := newTestServer(t)
	seedList(t, ts, "Team", []listEntry{
		{Name: "Ada", PickCount: 2, Tags: []string{"backend"}},
		{Name: "Bob", PickCount: 1, Tags: []string{"backend"}},
		{Name: "Cy", PickCount: 2, Tags: []string{"backend"}},
		{Name: "Dee", PickCount: 1, Tags: []string{"frontend"}},
		{Name: "Eve", PickCount: 0, Excluded: true},
		{Name: "Fin", PickCount: 2, Tags: []string{"frontend"}},
	})

	cases := []struct {
		name            string
		mustHave, anyOf []string
		want            []string
	}{
		{"empty filter", nil, nil, []string{"Bob", "Dee"}},
		{"must-have", []string{"frontend"}, nil, []string{"Dee"}},
		{"any-of", nil, []string{"backend"}, []string{"Bob"}},
		{"nobody eligible", []string{"nobody"}, nil, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := tableFor(t, ts, c.mustHave, c.anyOf)
			if got := marked(rows, inPot); !reflect.DeepEqual(got, c.want) {
				t.Errorf("in Pot = %v, want %v", got, c.want)
			}
		})
	}

	// Once Dee is picked the frontend Pot is everyone on two Picks.
	postJSON(t, ts, "/api/draw", `{"must_have":["frontend"]}`)
	if got := marked(tableFor(t, ts, []string{"frontend"}, nil), inPot); !reflect.DeepEqual(got, []string{"Dee", "Fin"}) {
		t.Errorf("after a Draw, in Pot = %v, want [Dee Fin]", got)
	}
}
