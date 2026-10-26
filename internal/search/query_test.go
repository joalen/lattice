package search_test

import (
	"reflect"
	"testing"

	"github.com/joalen/lattice/internal/fixture"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func f64(v float64) *float64 { return &v }

func find(t *testing.T, q search.Query) search.Result {
	t.Helper()
	store := mem.New()
	fixture.Index(t, store)
	q.Entity = "user"
	res, err := search.Find(t.Context(), store, q)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFind(t *testing.T) {
	cases := []struct {
		name string
		q    search.Query
		want []string
	}{
		{"no filters returns everything", search.Query{}, []string{"1", "2", "3", "4", "5", "6"}},
		{"single facet", search.Query{Facets: []search.Facet{{Path: "role", Values: []string{"student"}}}}, []string{"2", "3", "5"}},
		{"value is normalized", search.Query{Facets: []search.Facet{{Path: "address.city", Values: []string{"GWENBOROUGH"}}}}, []string{"1", "3", "5"}},
		{"OR within a facet", search.Query{Facets: []search.Facet{{Path: "role", Values: []string{"admin", "teacher"}}}}, []string{"1", "4", "6"}},
		{"AND across facets", search.Query{Facets: []search.Facet{
			{Path: "role", Values: []string{"student", "teacher"}},
			{Path: "address.city", Values: []string{"gwenborough"}},
		}}, []string{"3", "5"}},
		{"multi-valued field", search.Query{Facets: []search.Facet{{Path: "tags", Values: []string{"sql"}}}}, []string{"3", "4"}},
		{"NOT", search.Query{
			Facets: []search.Facet{{Path: "role", Values: []string{"student"}}},
			Not:    []search.Facet{{Path: "active", Values: []string{"false"}}},
		}, []string{"2", "5"}},
		{"NOT with several values", search.Query{
			Not: []search.Facet{{Path: "role", Values: []string{"student", "admin"}}},
		}, []string{"4"}},
		{"range is inclusive", search.Query{Ranges: []search.Range{{Path: "age", Min: f64(20), Max: f64(36)}}}, []string{"1", "2", "3"}},
		{"open-ended range", search.Query{Ranges: []search.Range{{Path: "age", Min: f64(45)}}}, []string{"4", "6"}},
		{"facet AND range AND NOT", search.Query{
			Facets: []search.Facet{{Path: "role", Values: []string{"student", "admin"}}},
			Ranges: []search.Range{{Path: "age", Max: f64(40)}},
			Not:    []search.Facet{{Path: "tags", Values: []string{"sql"}}},
		}, []string{"1", "2", "5"}},
		{"nothing matches", search.Query{Facets: []search.Facet{{Path: "role", Values: []string{"dean"}}}}, []string{}},
		{"empty range", search.Query{Ranges: []search.Range{{Path: "age", Min: f64(100)}}}, []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := find(t, tc.q)
			if !reflect.DeepEqual(append([]string{}, got.IDs...), tc.want) {
				t.Errorf("ids = %v, want %v", got.IDs, tc.want)
			}
			if got.Total != len(tc.want) {
				t.Errorf("total = %d, want %d", got.Total, len(tc.want))
			}
		})
	}
}

func TestFind_SortAndPaginate(t *testing.T) {
	byAge := &search.Sort{Path: "age"}

	if got := find(t, search.Query{Sort: byAge}); !reflect.DeepEqual(got.IDs, []string{"5", "2", "3", "1", "4", "6"}) {
		t.Errorf("asc = %v", got.IDs)
	}
	if got := find(t, search.Query{Sort: &search.Sort{Path: "age", Desc: true}}); !reflect.DeepEqual(got.IDs, []string{"6", "4", "1", "3", "2", "5"}) {
		t.Errorf("desc = %v", got.IDs)
	}

	page := find(t, search.Query{Sort: byAge, Offset: 2, Limit: 2})
	if !reflect.DeepEqual(page.IDs, []string{"3", "1"}) || page.Total != 6 {
		t.Errorf("page = %+v", page)
	}
	if past := find(t, search.Query{Sort: byAge, Offset: 10}); len(past.IDs) != 0 || past.Total != 6 {
		t.Errorf("past the end = %+v", past)
	}
	if tail := find(t, search.Query{Sort: byAge, Offset: 5, Limit: 10}); !reflect.DeepEqual(tail.IDs, []string{"6"}) {
		t.Errorf("tail = %v", tail.IDs)
	}
}

func TestFind_LeavesNoTempKeysAndDoesNotLeakBetweenQueries(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	multi := search.Query{Entity: "user", Facets: []search.Facet{{Path: "role", Values: []string{"admin", "teacher"}}}}
	for i := 0; i < 2; i++ {
		if _, err := search.Find(t.Context(), store, multi); err != nil {
			t.Fatal(err)
		}
	}

	res, err := search.Find(t.Context(), store, search.Query{
		Entity: "user",
		Facets: []search.Facet{{Path: "role", Values: []string{"student", "teacher"}}},
		Not:    []search.Facet{{Path: "active", Values: []string{"false"}}},
		Ranges: []search.Range{{Path: "age", Min: f64(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.IDs, []string{"2", "4", "5"}) {
		t.Errorf("ids = %v", res.IDs)
	}

	if left, _ := store.Keys(t.Context(), "user:_tmp:"); len(left) != 0 {
		t.Errorf("temp keys left behind: %v", left)
	}
}

func TestFind_Errors(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	if _, err := search.Find(t.Context(), store, search.Query{}); err == nil {
		t.Error("a query without an entity should fail")
	}
	if _, err := search.Find(t.Context(), store, search.Query{Entity: "user", Offset: -1}); err == nil {
		t.Error("a negative offset should fail")
	}
}
