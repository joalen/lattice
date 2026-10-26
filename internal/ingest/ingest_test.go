package ingest_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/joalen/lattice/internal/ingest"
)

const users = `[
 {"id":1,"name":"Ada","role":"admin","active":true,"age":36,"address":{"city":"Gwenborough"},"tags":["go","redis"],"nickname":null,"orders":[{"sku":"a"}]},
 {"id":2,"name":"Bob","role":"student","active":true,"age":20,"address":{"city":"Wisokyburgh"},"tags":["go"],"orders":[]},
 {"id":3,"name":"Cy","role":"student","active":false,"age":22,"address":{"city":"Gwenborough"},"tags":["sql","redis"]},
 {"id":4,"name":"Di","role":"teacher","active":true,"age":45,"address":{"city":"Wisokyburgh"},"tags":["go","sql"]},
 {"id":5,"name":"Ed","role":"student","active":true,"age":19,"address":{"city":"Gwenborough"},"tags":["redis"]},
 {"id":6,"name":"Flo","role":"admin","active":false,"age":51,"address":{"city":"McKenziehaven"},"tags":[]}
]`

func mustDocs(t *testing.T, raw string) []json.RawMessage {
	t.Helper()
	docs, err := ingest.SplitDocuments([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func TestFlatten(t *testing.T) {
	raw := `{"a":{"b":"x","n":1234567890123456789,"t":true,"z":null},
	         "tags":["p","q",null],"objs":[{"k":1}],"mixed":[1,[2]],"empty":[]}`

	flat, err := ingest.Flatten([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if got := flat["a.b"]; got.Array || len(got.Values) != 1 || got.Values[0] != (ingest.Scalar{Kind: ingest.KindString, Text: "x"}) {
		t.Errorf("a.b = %+v", got)
	}
	if got := flat["a.n"].Values[0]; got.Kind != ingest.KindNumber || got.Text != "1234567890123456789" {
		t.Errorf("a.n = %+v", got)
	}
	if got := flat["a.t"].Values[0]; got.Kind != ingest.KindBool || got.Text != "true" {
		t.Errorf("a.t = %+v", got)
	}
	if tags := flat["tags"]; !tags.Array || len(tags.Values) != 2 {
		t.Errorf("tags = %+v (null should be skipped)", tags)
	}
	for _, absent := range []string{"a.z", "objs", "mixed", "empty", "objs.k"} {
		if _, ok := flat[absent]; ok {
			t.Errorf("path %q should not be indexed", absent)
		}
	}
}

func TestFlatten_Rejects(t *testing.T) {
	for _, bad := range []string{`[1,2]`, `"str"`, `{"a":1} {"b":2}`, `{"a":`, ``} {
		if _, err := ingest.Flatten([]byte(bad)); err == nil {
			t.Errorf("Flatten(%q): expected an error", bad)
		}
	}
}

func TestProposeMapping(t *testing.T) {
	m, err := ingest.ProposeMapping("user", mustDocs(t, users), ingest.ProposeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if m.ID != "id" {
		t.Errorf("guessed id = %q, want id", m.ID)
	}
	want := map[string]ingest.IndexType{
		"id":           ingest.IndexSkip,
		"name":         ingest.IndexSkip,
		"role":         ingest.IndexSet,
		"active":       ingest.IndexSet,
		"age":          ingest.IndexRange,
		"address.city": ingest.IndexSet,
		"tags":         ingest.IndexSet,
	}
	for path, typ := range want {
		if m.Fields[path] != typ {
			t.Errorf("%s: got %q, want %q", path, m.Fields[path], typ)
		}
	}
	if _, ok := m.Fields["orders"]; ok {
		t.Error("array of objects must not appear in the mapping")
	}
}

func TestProposeMapping_NoGuessableID(t *testing.T) {
	_, err := ingest.ProposeMapping("x", mustDocs(t, `[{"a":"1"},{"a":"1"}]`), ingest.ProposeOptions{})
	if err == nil || !strings.Contains(err.Error(), "could not guess an id") {
		t.Fatalf("got %v", err)
	}
	if _, err := ingest.ProposeMapping("x", mustDocs(t, `[{"k":"1"},{"k":"2"}]`), ingest.ProposeOptions{ID: "k"}); err != nil {
		t.Fatal(err)
	}
}

func TestMapping_RoundTripAndValidation(t *testing.T) {
	m, err := ingest.ProposeMapping("user", mustDocs(t, users), ingest.ProposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ingest.ParseMapping(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, back) {
		t.Errorf("round trip mismatch:\n%+v\n%+v", m, back)
	}

	for name, bad := range map[string]string{
		"typo key":     `{"entity":"u","id":"id","feilds":{}}`,
		"no entity":    `{"id":"id","fields":{}}`,
		"colon entity": `{"entity":"a:b","id":"id","fields":{}}`,
		"underscore":   `{"entity":"_a","id":"id","fields":{}}`,
		"bad type":     `{"entity":"u","id":"id","fields":{"x":"fulltext"}}`,
	} {
		if _, err := ingest.ParseMapping(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSplitAndLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"plans/a.json":        {Data: []byte(`{"id":"a"}`)},
		"plans/nested/b.JSON": {Data: []byte(`[{"id":"b"},{"id":"c"}]`)},
		"plans/readme.md":     {Data: []byte(`ignored`)},
	}
	docs, err := ingest.LoadFS(fsys, "plans")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}

	if _, err := ingest.LoadFS(fstest.MapFS{"x.txt": {Data: []byte("hi")}}, "."); err == nil {
		t.Error("a tree with no JSON should be an error")
	}
	if _, err := ingest.SplitDocuments([]byte(`42`)); err == nil {
		t.Error("a bare number is not a document")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.json"), []byte(`{"id":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ingest.LoadPath(dir); err != nil || len(got) != 1 {
		t.Errorf("LoadPath(dir) = %d docs, %v", len(got), err)
	}
	if got, err := ingest.LoadPath(filepath.Join(dir, "one.json")); err != nil || len(got) != 1 {
		t.Errorf("LoadPath(file) = %d docs, %v", len(got), err)
	}
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
