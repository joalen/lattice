package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joalen/lattice/internal/cli"
	"github.com/joalen/lattice/internal/fixture"
	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/setstore/mem"
)

type harness struct {
	t     *testing.T
	store *mem.Store
	dir   string
}

func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code = cli.Run(args, &out, &errb, func(addr, prefix string) (ports.SetStore, func() error, error) {
		return h.store, func() error { return nil }, nil
	})
	return out.String(), errb.String(), code
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, errOut, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("lattice %v exited %d: %s", args, code, errOut)
	}
	return out
}

func (h *harness) write(name, content string) string {
	h.t.Helper()
	p := filepath.Join(h.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func newIndexed(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: mem.New(), dir: t.TempDir()}
	users := h.write("users.json", fixture.Users)
	mapping := h.write("mapping.json", h.mustRun("propose", "--entity", "user", users))
	h.mustRun("ingest", "--mapping", mapping, users)
	return h
}

type searchOut struct {
	Total   int `json:"total"`
	Offset  int `json:"offset"`
	Results []struct {
		ID      string         `json:"id"`
		Summary map[string]any `json:"summary"`
	} `json:"results"`
	Facets map[string][]struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	} `json:"facets"`
}

func (s searchOut) ids() []string {
	out := make([]string, len(s.Results))
	for i, r := range s.Results {
		out[i] = r.ID
	}
	return out
}

func (h *harness) search(args ...string) searchOut {
	h.t.Helper()
	var s searchOut
	raw := h.mustRun(append([]string{"search", "--entity", "user"}, args...)...)
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		h.t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	return s
}

func TestProposeIngestRoundTrip(t *testing.T) {
	h := &harness{t: t, store: mem.New(), dir: t.TempDir()}
	users := h.write("users.json", fixture.Users)

	out := h.mustRun("propose", "--entity", "user", users)
	m, err := ingest.ParseMapping(strings.NewReader(out))
	if err != nil {
		t.Fatalf("propose output is not a valid mapping: %v\n%s", err, out)
	}
	if m.Entity != "user" || m.ID != "id" || m.Fields["role"] != ingest.IndexSet || m.Fields["age"] != ingest.IndexRange {
		t.Errorf("mapping = %+v", m)
	}

	mapping := h.write("mapping.json", out)
	var rep struct{ Docs, Created, Unchanged int }
	if err := json.Unmarshal([]byte(h.mustRun("ingest", "--mapping", mapping, users)), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Docs != 6 || rep.Created != 6 {
		t.Errorf("first ingest = %+v", rep)
	}
	rep = struct{ Docs, Created, Unchanged int }{}
	if err := json.Unmarshal([]byte(h.mustRun("ingest", "--mapping", mapping, users)), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Unchanged != 6 || rep.Created != 0 {
		t.Errorf("second ingest = %+v", rep)
	}
}

func TestIngestPrune(t *testing.T) {
	h := newIndexed(t)
	docs, err := ingest.SplitDocuments([]byte(fixture.Users))
	if err != nil {
		t.Fatal(err)
	}
	subset, _ := json.Marshal(docs[:5])
	part := h.write("part.json", string(subset))

	var rep struct{ Pruned int }
	raw := h.mustRun("ingest", "--prune", "--mapping", filepath.Join(h.dir, "mapping.json"), part)
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Pruned != 1 {
		t.Errorf("pruned = %d, want 1", rep.Pruned)
	}
	if _, _, code := h.run("get", "--entity", "user", "6"); code != 1 {
		t.Errorf("user 6 should be gone, exit %d", code)
	}
}

func TestSearch(t *testing.T) {
	h := newIndexed(t)

	cases := []struct {
		name  string
		args  []string
		ids   []string
		total int
	}{
		{"facet and not", []string{"--facet", "role=student", "--not", "active=false"}, []string{"2", "5"}, 2},
		{"OR within a facet", []string{"--facet", "role=admin,teacher"}, []string{"1", "4", "6"}, 3},
		{"repeated facets AND", []string{"--facet", "role=student", "--facet", "address.city=Gwenborough"}, []string{"3", "5"}, 2},
		{"range with sort and limit", []string{"--range", "age=20..40", "--sort", "age:desc", "--limit", "2"}, []string{"1", "3"}, 3},
		{"open-ended range", []string{"--range", "age=45.."}, []string{"4", "6"}, 2},
		{"page two", []string{"--sort", "age", "--limit", "2", "--offset", "2"}, []string{"3", "1"}, 6},
		{"no limit", []string{"--limit", "0"}, []string{"1", "2", "3", "4", "5", "6"}, 6},
		{"no match", []string{"--facet", "role=dean"}, []string{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.search(tc.args...)
			if !reflect.DeepEqual(got.ids(), tc.ids) || got.Total != tc.total {
				t.Errorf("ids = %v total = %d, want %v / %d", got.ids(), got.Total, tc.ids, tc.total)
			}
		})
	}
}

func TestSearch_FacetsCoverAllMatchesNotJustThePage(t *testing.T) {
	h := newIndexed(t)

	got := h.search("--facet", "role=student", "--limit", "1", "--facets", "role,tags")
	if len(got.Results) != 1 || got.Total != 3 {
		t.Fatalf("page = %v total %d", got.ids(), got.Total)
	}
	if c := got.Facets["role"]; len(c) != 1 || c[0].Value != "student" || c[0].Count != 3 {
		t.Errorf("role counts = %+v", c)
	}
	tags := got.Facets["tags"]
	if len(tags) != 3 || tags[0].Value != "redis" || tags[0].Count != 2 {
		t.Errorf("tag counts = %+v", tags)
	}
}

func TestSearch_Summaries(t *testing.T) {
	h := newIndexed(t)

	def := h.search("--facet", "role=admin", "--limit", "1").Results[0].Summary
	keys := make([]string, 0, len(def))
	for k := range def {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"active", "age", "id", "name", "role"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("default summary keys = %v, want %v", keys, want)
	}
	if def["age"] != float64(36) || def["active"] != true || def["name"] != "Ada" {
		t.Errorf("default summary = %v", def)
	}

	picked := h.search("--facet", "role=admin", "--limit", "1", "--fields", "name,address.city,tags,missing").Results[0].Summary
	want := map[string]any{"name": "Ada", "address.city": "Gwenborough", "tags": []any{"go", "redis"}}
	if !reflect.DeepEqual(picked, want) {
		t.Errorf("selected summary = %v, want %v", picked, want)
	}
}

func TestSummaryTruncatesLongStrings(t *testing.T) {
	h := &harness{t: t, store: mem.New(), dir: t.TempDir()}
	docs := h.write("d.json", `[{"id":"x","bio":"`+strings.Repeat("é", 300)+`"}]`)
	mapping := h.write("m.json", `{"entity":"doc","id":"id","fields":{}}`)
	h.mustRun("ingest", "--mapping", mapping, docs)

	var s searchOut
	if err := json.Unmarshal([]byte(h.mustRun("search", "--entity", "doc")), &s); err != nil {
		t.Fatal(err)
	}
	bio, _ := s.Results[0].Summary["bio"].(string)
	if utf8.RuneCountInString(bio) != 121 || !strings.HasSuffix(bio, "…") {
		t.Errorf("bio has %d runes, want 120 plus an ellipsis", utf8.RuneCountInString(bio))
	}

	full := h.mustRun("get", "--entity", "doc", "x")
	if strings.Count(full, "é") != 300 {
		t.Error("get must return the untruncated document")
	}
}

func TestCompactOutput(t *testing.T) {
	h := newIndexed(t)
	out := h.mustRun("search", "--entity", "user", "--compact")
	if strings.Count(out, "\n") != 1 {
		t.Errorf("compact output spans multiple lines:\n%s", out)
	}
}

func TestGet(t *testing.T) {
	h := newIndexed(t)

	out := h.mustRun("get", "--entity", "user", "3")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["name"] != "Cy" {
		t.Errorf("doc = %v", doc)
	}

	_, errOut, code := h.run("get", "--entity", "user", "999")
	if code != 1 || !strings.Contains(errOut, "no document") {
		t.Errorf("missing doc: exit %d, stderr %q", code, errOut)
	}
}

func TestSimilar(t *testing.T) {
	h := newIndexed(t)

	var got struct {
		ID      string `json:"id"`
		Matches []struct {
			ID      string         `json:"id"`
			Score   float64        `json:"score"`
			Shared  int            `json:"shared"`
			Summary map[string]any `json:"summary"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(h.mustRun("similar", "--entity", "user", "--limit", "2", "1")), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "1" || len(got.Matches) != 2 {
		t.Fatalf("got %+v", got)
	}
	if top := got.Matches[0]; top.ID != "5" || top.Score != 0.5 || top.Shared != 3 || top.Summary["name"] != "Ed" {
		t.Errorf("top match = %+v", top)
	}

	_, errOut, code := h.run("similar", "--entity", "user", "999")
	if code != 1 || !strings.Contains(errOut, "no document") {
		t.Errorf("unknown id: exit %d, stderr %q", code, errOut)
	}
}

func TestConnectionFlagsReachTheOpener(t *testing.T) {
	var gotAddr, gotPrefix string
	var out, errb bytes.Buffer
	code := cli.Run([]string{"get", "--redis", "db.internal:6380", "--prefix", "dev:", "--entity", "user", "1"}, &out, &errb,
		func(addr, prefix string) (ports.SetStore, func() error, error) {
			gotAddr, gotPrefix = addr, prefix
			return mem.New(), func() error { return nil }, nil
		})
	if gotAddr != "db.internal:6380" || gotPrefix != "dev:" {
		t.Errorf("opener got addr %q prefix %q (exit %d: %s)", gotAddr, gotPrefix, code, errb.String())
	}
}

func TestExitCodesAndMessages(t *testing.T) {
	h := newIndexed(t)
	users := filepath.Join(h.dir, "users.json")

	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no command", nil, 2, "Usage"},
		{"unknown command", []string{"bogus"}, 2, "unknown command"},
		{"unknown flag", []string{"search", "--bogus"}, 2, "flag provided but not defined"},
		{"search without entity", []string{"search"}, 2, "--entity is required"},
		{"stray argument", []string{"search", "--entity", "user", "oops"}, 2, "unexpected arguments"},
		{"facet without value", []string{"search", "--entity", "user", "--facet", "role"}, 2, "bad facet"},
		{"unparseable range", []string{"search", "--entity", "user", "--range", "age=abc..3"}, 2, "bad range"},
		{"range without dots", []string{"search", "--entity", "user", "--range", "age=30"}, 2, "bad range"},
		{"inverted range", []string{"search", "--entity", "user", "--range", "age=40..20"}, 2, "above upper bound"},
		{"range on non-numeric field", []string{"search", "--entity", "user", "--range", "name=1..2"}, 2, `field "name" is mapped as "skip", not "range"`},
		{"sort on non-numeric field", []string{"search", "--entity", "user", "--sort", "role"}, 2, "numeric fields: age"},
		{"unknown facet field", []string{"search", "--entity", "user", "--facet", "rol=student"}, 2, `unknown field "rol" (facet fields: active, address.city, role, tags)`},
		{"unknown not field", []string{"search", "--entity", "user", "--not", "rol=student"}, 2, "unknown field"},
		{"facet on an unindexed field", []string{"search", "--entity", "user", "--facet", "name=Ada"}, 2, `mapped as "skip", not "set"`},
		{"facet on a numeric field", []string{"search", "--entity", "user", "--facet", "age=36"}, 2, `mapped as "range", not "set"`},
		{"unknown --facets field", []string{"search", "--entity", "user", "--facets", "nope"}, 2, "unknown field"},
		{"unknown similar --paths field", []string{"similar", "--entity", "user", "--paths", "nope", "1"}, 2, "unknown field"},
		{"entity never ingested", []string{"search", "--entity", "ghost"}, 1, "has not been ingested"},
		{"bad sort direction", []string{"search", "--entity", "user", "--sort", "age:up"}, 2, "bad sort"},
		{"negative limit", []string{"search", "--entity", "user", "--limit", "-1"}, 2, "must not be negative"},
		{"get without id", []string{"get", "--entity", "user"}, 2, "exactly one ID"},
		{"ingest without mapping", []string{"ingest", users}, 2, "--mapping is required"},
		{"propose without entity", []string{"propose", users}, 2, "--entity is required"},
		{"ingest missing mapping file", []string{"ingest", "--mapping", filepath.Join(h.dir, "nope.json"), users}, 1, "no such file"},
		{"propose missing input", []string{"propose", "--entity", "user", filepath.Join(h.dir, "nope.json")}, 1, "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, code := h.run(tc.args...)
			if code != tc.code || !strings.Contains(errOut, tc.msg) {
				t.Errorf("exit %d, stderr %q; want exit %d containing %q", code, errOut, tc.code, tc.msg)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	h := newIndexed(t)

	if out, _, code := h.run("help"); code != 0 || !strings.Contains(out, "lattice search") {
		t.Errorf("help: exit %d, stdout %q", code, out)
	}
	if _, errOut, code := h.run("search", "-h"); code != 0 || !strings.Contains(errOut, "-facet") {
		t.Errorf("search -h: exit %d, stderr %q", code, errOut)
	}
}
