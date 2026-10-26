package seed_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/seed"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func TestItems_DeterministicAndWellFormed(t *testing.T) {
	a, b := seed.Items(500, 7), seed.Items(500, 7)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different items")
	}
	if reflect.DeepEqual(a, seed.Items(500, 8)) {
		t.Fatal("different seeds produced identical items")
	}

	for i, it := range a {
		if it.ID != i+1 {
			t.Fatalf("item %d has id %d", i, it.ID)
		}
		if it.Year < seed.MinYear || it.Year > seed.MaxYear || it.Score < 0 || it.Score > seed.MaxScore {
			t.Fatalf("item %d out of range: %+v", it.ID, it)
		}
		if len(it.Tags) < 3 || len(it.Tags) > 8 {
			t.Fatalf("item %d has %d tags", it.ID, len(it.Tags))
		}
		uniq := map[string]bool{}
		for _, tag := range it.Tags {
			uniq[tag] = true
		}
		if len(uniq) != len(it.Tags) {
			t.Fatalf("item %d has duplicate tags: %v", it.ID, it.Tags)
		}
	}
}

func TestItems_GroupsAreSkewed(t *testing.T) {
	counts := map[string]int{}
	items := seed.Items(5000, 1)
	for _, it := range items {
		counts[it.Group]++
	}
	if avg := len(items) / len(counts); counts["group-00"] < 3*avg {
		t.Errorf("group-00 has %d items, want well above the average of %d", counts["group-00"], avg)
	}
}

func TestMappingIndexesGeneratedItems(t *testing.T) {
	m := seed.Mapping()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}

	store := mem.New()
	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := b.Ingest(t.Context(), seed.JSON(seed.Items(500, 3)), ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 500 {
		t.Fatalf("report = %+v", rep)
	}

	if n, _ := store.Cardinality(t.Context(), keyspace.Universe(seed.Entity)); n != 500 {
		t.Errorf("universe = %d", n)
	}
	for _, path := range []string{"year", "score"} {
		ids, _ := store.RangeByScore(t.Context(), keyspace.Range(seed.Entity, path), -1, 1e9)
		if len(ids) != 500 {
			t.Errorf("%s index holds %d items", path, len(ids))
		}
	}
	keys, _ := store.Keys(t.Context(), keyspace.FacetPrefix(seed.Entity, "owner.team"))
	if len(keys) == 0 || !strings.HasPrefix(keys[0], "item:owner.team:team-") {
		t.Errorf("nested path not indexed: %v", keys)
	}
}
