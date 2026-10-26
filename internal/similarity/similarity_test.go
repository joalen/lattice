package similarity_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/joalen/lattice/internal/fixture"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/setstore/mem"
	"github.com/joalen/lattice/internal/similarity"
)

func set(vals ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		a, b map[string]struct{}
		want float64
	}{
		{set("a", "b", "c"), set("b", "c", "d"), 0.5},
		{set("a"), set("a"), 1},
		{set("a"), set("b"), 0},
		{set(), set(), 0},
		{set("a", "b"), set(), 0},
	}
	for i, tc := range cases {
		if got := similarity.Jaccard(tc.a, tc.b); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("case %d: got %v, want %v", i, got, tc.want)
		}
		if got := similarity.Jaccard(tc.b, tc.a); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("case %d reversed: got %v, want %v", i, got, tc.want)
		}
	}
}

func TestSimilar(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	got, err := similarity.Similar(t.Context(), store, "user", "1", similarity.Options{})
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.ID
	}
	if want := []string{"5", "2", "3", "4", "6"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	if got[0].Score != 0.5 || got[0].Shared != 3 {
		t.Errorf("top match = %+v, want score 0.5 and 3 shared", got[0])
	}
	if math.Abs(got[1].Score-2.0/7) > 1e-12 {
		t.Errorf("second score = %v, want 2/7", got[1].Score)
	}

	top, _ := similarity.Similar(t.Context(), store, "user", "1", similarity.Options{Limit: 2})
	if len(top) != 2 || top[0].ID != "5" {
		t.Errorf("limit: %+v", top)
	}
}

func TestSimilar_RestrictedToPaths(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	got, err := similarity.Similar(t.Context(), store, "user", "1", similarity.Options{Paths: []string{"tags"}})
	if err != nil {
		t.Fatal(err)
	}
	// user 1 has tags {go, redis}: user 5 {redis} = 1/2, users 2 {go} = 1/2, user 3 {redis, sql} = 1/3, user 4 {go, sql} = 1/3
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.ID
	}
	if want := []string{"2", "5", "3", "4"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestSimilar_UnknownID(t *testing.T) {
	store := mem.New()
	fixture.Index(t, store)

	if _, err := similarity.Similar(t.Context(), store, "user", "999", similarity.Options{}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}
