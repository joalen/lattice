package ingest_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func newBuilder(t *testing.T, store ports.SetStore, docs []json.RawMessage) *ingest.Builder {
	t.Helper()
	m, err := ingest.ProposeMapping("user", docs, ingest.ProposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func members(t *testing.T, s ports.SetStore, key string) []string {
	t.Helper()
	got, err := s.Members(key)
	if err != nil {
		t.Fatal(err)
	}
	return sorted(got)
}

func TestIngest_BuildsIndexes(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)

	rep, err := b.Ingest(docs, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Docs != 6 || rep.Created != 6 || rep.Unchanged != 0 || rep.Updated != 0 {
		t.Errorf("report = %+v", rep)
	}

	if got := members(t, store, keyspace.Universe("user")); len(got) != 6 {
		t.Errorf("universe = %v", got)
	}
	students := members(t, store, keyspace.Facet("user", "role", "student"))
	if !reflect.DeepEqual(students, []string{"2", "3", "5"}) {
		t.Errorf("students = %v", students)
	}

	for _, tag := range []string{"go", "redis"} {
		if got := members(t, store, keyspace.Facet("user", "tags", tag)); !contains(got, "1") {
			t.Errorf("user 1 missing from tag %q: %v", tag, got)
		}
	}

	both, err := store.Intersect(
		keyspace.Facet("user", "role", "student"),
		keyspace.Facet("user", "address.city", "Gwenborough"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sorted(both), []string{"3", "5"}) {
		t.Errorf("students in Gwenborough = %v", both)
	}

	raw, err := store.GetDocument("user", "3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"Cy"`) {
		t.Errorf("document = %s", raw)
	}
}

func TestIngest_IsIdempotent(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)

	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Ingest(docs, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unchanged != 6 || rep.Created != 0 || rep.Updated != 0 || rep.KeysAdded != 0 || rep.KeysRemoved != 0 {
		t.Errorf("second run should change nothing, got %+v", rep)
	}
}

func TestIngest_UpdateMovesMemberships(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	changed := strings.Replace(users,
		`{"id":2,"name":"Bob","role":"student","active":true,"age":20,"address":{"city":"Wisokyburgh"},"tags":["go"],"orders":[]}`,
		`{"id":2,"name":"Bob","role":"teacher","active":true,"age":20,"address":{"city":"Wisokyburgh"},"tags":["go","sql"],"orders":[]}`, 1)
	if changed == users {
		t.Fatal("test fixture replacement did not apply")
	}

	rep, err := b.Ingest(mustDocs(t, changed), ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 || rep.Unchanged != 5 || rep.Created != 0 {
		t.Errorf("report = %+v", rep)
	}
	if rep.KeysAdded != 2 || rep.KeysRemoved != 1 {
		t.Errorf("keys added/removed = %d/%d, want 2/1", rep.KeysAdded, rep.KeysRemoved)
	}

	if got := members(t, store, keyspace.Facet("user", "role", "student")); contains(got, "2") {
		t.Errorf("user 2 still a student: %v", got)
	}
	if got := members(t, store, keyspace.Facet("user", "role", "teacher")); !contains(got, "2") {
		t.Errorf("user 2 not a teacher: %v", got)
	}
	if got := members(t, store, keyspace.Facet("user", "tags", "sql")); !contains(got, "2") {
		t.Errorf("user 2 not tagged sql: %v", got)
	}
}

func TestIngest_Prune(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	subset := docs[:5]

	if rep, err := b.Ingest(subset, ingest.Options{}); err != nil || rep.Pruned != 0 {
		t.Fatalf("no-prune run: %+v, %v", rep, err)
	}
	if _, err := store.GetDocument("user", "6"); err != nil {
		t.Fatalf("user 6 should still exist: %v", err)
	}

	rep, err := b.Ingest(subset, ingest.Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", rep.Pruned)
	}
	if _, err := store.GetDocument("user", "6"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("user 6 document: got %v, want ErrNotFound", err)
	}
	if got := members(t, store, keyspace.Universe("user")); contains(got, "6") {
		t.Errorf("user 6 still in universe: %v", got)
	}
	if got := members(t, store, keyspace.Facet("user", "role", "admin")); contains(got, "6") {
		t.Errorf("user 6 still an admin: %v", got)
	}
	if n, _ := store.Cardinality(keyspace.Facet("user", "address.city", "McKenziehaven")); n != 0 {
		t.Errorf("empty facet set should be gone, cardinality = %d", n)
	}
	if n, _ := store.Cardinality(keyspace.Memberships("user", "6")); n != 0 {
		t.Errorf("reverse index for user 6 should be gone, cardinality = %d", n)
	}
}

func TestIngest_FailsFastWithoutPartialWrites(t *testing.T) {
	cases := map[string]string{
		"duplicate id": `[{"id":1,"role":"a"},{"id":2,"role":"a"},{"id":1,"role":"b"}]`,
		"missing id":   `[{"id":1,"role":"a"},{"role":"b"}]`,
		"array id":     `[{"id":1,"role":"a"},{"id":[1,2],"role":"b"}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			store := mem.New()
			m := ingest.Mapping{Entity: "user", ID: "id", Fields: map[string]ingest.IndexType{"role": ingest.IndexSet}}
			b, err := ingest.NewBuilder(store, m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Ingest(mustDocs(t, raw), ingest.Options{}); err == nil {
				t.Fatal("expected an error")
			}
			if n, _ := store.Cardinality(keyspace.Universe("user")); n != 0 {
				t.Errorf("nothing should have been written, universe has %d", n)
			}
			if _, err := ingest.LoadMapping(store, "user"); !errors.Is(err, ports.ErrNotFound) {
				t.Errorf("mapping saved despite failure: %v", err)
			}
		})
	}
}

func TestNewBuilder_RejectsInvalidMapping(t *testing.T) {
	if _, err := ingest.NewBuilder(mem.New(), ingest.Mapping{}); err == nil {
		t.Fatal("expected an error")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func ageOrder(t *testing.T, s ports.SetStore, min, max float64) []string {
	t.Helper()
	got, err := s.RangeByScore(keyspace.Range("user", "age"), min, max)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestIngest_IndexesRanges(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	if _, err := newBuilder(t, store, docs).Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	if got := ageOrder(t, store, math.Inf(-1), math.Inf(1)); !reflect.DeepEqual(got, []string{"5", "2", "3", "1", "4", "6"}) {
		t.Errorf("all by age = %v", got)
	}
	if got := ageOrder(t, store, 20, 40); !reflect.DeepEqual(got, []string{"2", "3", "1"}) {
		t.Errorf("age 20..40 = %v", got)
	}
}

func TestIngest_RangeFollowsUpdatesAndPrune(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	changed := strings.Replace(users, `"name":"Bob","role":"student","active":true,"age":20,`, `"name":"Bob","role":"student","active":true,"age":60,`, 1)
	if changed == users {
		t.Fatal("fixture replacement did not apply")
	}
	rep, err := b.Ingest(mustDocs(t, changed), ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 || rep.Unchanged != 5 {
		t.Errorf("report = %+v", rep)
	}
	if got := ageOrder(t, store, 20, 40); !reflect.DeepEqual(got, []string{"3", "1"}) {
		t.Errorf("age 20..40 after rescoring = %v", got)
	}
	if got := ageOrder(t, store, 60, 60); !reflect.DeepEqual(got, []string{"2"}) {
		t.Errorf("age 60 = %v", got)
	}

	noAge := strings.Replace(changed, `"name":"Bob","role":"student","active":true,"age":60,`, `"name":"Bob","role":"student","active":true,`, 1)
	if _, err := b.Ingest(mustDocs(t, noAge), ingest.Options{}); err != nil {
		t.Fatal(err)
	}
	if got := ageOrder(t, store, math.Inf(-1), math.Inf(1)); contains(got, "2") {
		t.Errorf("user 2 should have left the age index: %v", got)
	}

	if _, err := b.Ingest(mustDocs(t, noAge)[:3], ingest.Options{Prune: true}); err != nil {
		t.Fatal(err)
	}
	if got := ageOrder(t, store, math.Inf(-1), math.Inf(1)); !reflect.DeepEqual(got, []string{"3", "1"}) {
		t.Errorf("after prune = %v", got)
	}
}

func TestIngest_RangeValueMustBeANumber(t *testing.T) {
	store := mem.New()
	m := ingest.Mapping{Entity: "user", ID: "id", Fields: map[string]ingest.IndexType{"age": ingest.IndexRange}}
	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Ingest(mustDocs(t, `[{"id":1,"age":"old"}]`), ingest.Options{}); err == nil {
		t.Fatal("expected an error")
	}
	if n, _ := store.Cardinality(keyspace.Universe("user")); n != 0 {
		t.Errorf("nothing should have been written, universe has %d", n)
	}
}

func TestIngest_PersistsMapping(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	b := newBuilder(t, store, docs)

	if _, err := ingest.LoadMapping(store, "user"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("before ingest: got %v, want ErrNotFound", err)
	}
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}
	got, err := ingest.LoadMapping(store, "user")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ingest.ProposeMapping("user", docs, ingest.ProposeOptions{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

func TestIngest_RegistersFacetValues(t *testing.T) {
	store := mem.New()
	docs := mustDocs(t, users)
	if _, err := newBuilder(t, store, docs).Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string][]string{
		"role":         {"admin", "student", "teacher"},
		"tags":         {"go", "redis", "sql"},
		"address.city": {"gwenborough", "mckenziehaven", "wisokyburgh"},
	} {
		if got := members(t, store, keyspace.Values("user", path)); !reflect.DeepEqual(got, want) {
			t.Errorf("values for %s = %v, want %v", path, got, want)
		}
	}
}
