package facets_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joalen/lattice/internal/facets"
	"github.com/joalen/lattice/internal/fixture"
	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func TestCounts_WithinCurrentFilter(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	ids, err := search.Match(store, search.Query{
		Entity: "user",
		Facets: []search.Facet{{Path: "role", Values: []string{"student"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := facets.Counts(store, "user", []string{"active", "address.city", "tags", "role"}, ids)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]facets.Count{
		"active":       {{"true", 2}, {"false", 1}},
		"address.city": {{"gwenborough", 2}, {"wisokyburgh", 1}},
		"tags":         {{"redis", 2}, {"go", 1}, {"sql", 1}},
		"role":         {{"student", 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	if left, _ := store.Keys("user:_tmp:"); len(left) != 0 {
		t.Errorf("temp keys left behind: %v", left)
	}
}

func TestCounts_EmptyResultAndUnknownPath(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	got, err := facets.Counts(store, "user", []string{"role"}, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("empty ids: %v, %v", got, err)
	}

	got, err = facets.Counts(store, "user", []string{"nope"}, []string{"1", "2"})
	if err != nil || len(got) != 0 {
		t.Errorf("unknown path: %v, %v", got, err)
	}
}

func TestCounts_ValueThatLostAllItsDocumentsIsOmitted(t *testing.T) {
	store := mem.New()
	m := fixture.Index(t, store)
	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		t.Fatal(err)
	}

	moved := strings.Replace(fixture.Users, `"city":"McKenziehaven"`, `"city":"Gwenborough"`, 1)
	docs, err := ingest.SplitDocuments([]byte(moved))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}

	ids, err := search.Match(store, search.Query{Entity: "user"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := facets.Counts(store, "user", []string{"address.city"}, ids)
	if err != nil {
		t.Fatal(err)
	}
	want := []facets.Count{{Value: "gwenborough", N: 4}, {Value: "wisokyburgh", N: 2}}
	if !reflect.DeepEqual(got["address.city"], want) {
		t.Errorf("got %v, want %v", got["address.city"], want)
	}
}
