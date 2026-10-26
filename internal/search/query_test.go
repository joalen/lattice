package search_test

import (
	"sort"
	"testing"

	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func seed(t *testing.T) *mem.Store {
	t.Helper()
	s := mem.New()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddToSet("plan:major:cs", "1", "2", "3"))
	must(s.AddToSet("plan:major:math", "4", "5"))
	must(s.AddToSet("plan:degree:bs", "1", "4", "6"))
	must(s.AddToSet("plan:degree:ba", "2", "5"))
	return s
}

func sorted(ids []string) []string {
	sort.Strings(ids)
	return ids
}

func TestFind_ORWithinFacetANDAcross(t *testing.T) {
	s := seed(t)
	got, err := search.Find(s, search.Query{Facets: []search.FacetMatch{
		{Prefix: "plan:major:", Values: []string{"cs", "math"}},
		{Prefix: "plan:degree:", Values: []string{"bs"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "4"}
	if g := sorted(got); len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Fatalf("got %v, want %v", g, want)
	}
}

// Regression: a second query must not see leftovers from the first.
func TestFind_NoTempKeyLeakBetweenQueries(t *testing.T) {
	s := seed(t)
	q1 := search.Query{Facets: []search.FacetMatch{
		{Prefix: "plan:major:", Values: []string{"cs", "math"}},
	}}
	if _, err := search.Find(s, q1); err != nil {
		t.Fatal(err)
	}

	q2 := search.Query{Facets: []search.FacetMatch{
		{Prefix: "plan:major:", Values: []string{"math", "cs"}},
		{Prefix: "plan:degree:", Values: []string{"ba"}},
	}}
	got, _ := search.Find(s, q2)
	if g := sorted(got); len(g) != 2 || g[0] != "2" || g[1] != "5" {
		t.Fatalf("got %v, want [2 5]", g)
	}
}
