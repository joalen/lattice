package contract

import (
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/joalen/lattice/internal/ports"
)

// Factory returns a fresh, empty store
type Factory func(t *testing.T) ports.SetStore

// Run executes the whole suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		fn   func(t *testing.T, s ports.SetStore)
	}{
		{"AddToSet is idempotent", addIdempotent},
		{"AddToSet with no members is a no-op", addNoMembers},
		{"Cardinality of a missing key is zero", cardinalityMissing},
		{"Intersect", intersect},
		{"Intersect with a missing key is empty", intersectMissing},
		{"Intersect with no keys errors", intersectNoKeys},
		{"Union", union},
		{"Union ignores missing keys", unionMissing},
		{"Difference", difference},
		{"Difference with no keys errors", differenceNoKeys},
		{"UnionStore materializes and overwrites", unionStore},
		{"Delete removes sets and tolerates missing keys", deleteKeys},
		{"Documents round-trip", docRoundTrip},
		{"Documents: missing is an error, entities are isolated", docMissingAndIsolation},
		{"Set-theory laws hold", setLaws},
		{"Concurrent writers are safe", concurrentAdds},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, newStore(t))
		})
	}
}

// Sets are unordered (in Go maps and in Redis), so every comparison sorts first.
func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func mustAdd(t *testing.T, s ports.SetStore, key string, members ...string) {
	t.Helper()
	if err := s.AddToSet(key, members...); err != nil {
		t.Fatalf("AddToSet(%q): %v", key, err)
	}
}

func assertMembers(t *testing.T, got []string, want ...string) {
	t.Helper()
	g, w := sorted(got), sorted(want)
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %v, want %v", g, w)
	}
}

func assertCard(t *testing.T, s ports.SetStore, key string, want int) {
	t.Helper()
	n, err := s.Cardinality(key)
	if err != nil {
		t.Fatalf("Cardinality(%q): %v", key, err)
	}
	if n != want {
		t.Fatalf("Cardinality(%q) = %d, want %d", key, n, want)
	}
}

func addIdempotent(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2", "2")
	mustAdd(t, s, "a", "2", "3")
	assertCard(t, s, "a", 3)
}

// Redis rejects SADD with zero members, so an adapter has to guard for this.
func addNoMembers(t *testing.T, s ports.SetStore) {
	if err := s.AddToSet("a"); err != nil {
		t.Fatalf("AddToSet with no members should not error: %v", err)
	}
	assertCard(t, s, "a", 0)
}

func cardinalityMissing(t *testing.T, s ports.SetStore) {
	assertCard(t, s, "nope", 0)
}

func intersect(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2", "3")
	mustAdd(t, s, "b", "2", "3", "4")
	mustAdd(t, s, "c", "3", "4", "5")

	got, err := s.Intersect("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "2", "3")

	got, err = s.Intersect("a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "3")

	// A single key is just that set.
	got, err = s.Intersect("a")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1", "2", "3")
}

func intersectMissing(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2")

	// Missing key in either position behaves as the empty set.
	got, err := s.Intersect("a", "ghost")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got)

	got, err = s.Intersect("ghost", "a")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got)
}

func intersectNoKeys(t *testing.T, s ports.SetStore) {
	if _, err := s.Intersect(); err == nil {
		t.Fatal("expected an error for zero keys")
	}
}

func union(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2")
	mustAdd(t, s, "b", "2", "3")
	mustAdd(t, s, "c", "9")

	got, err := s.Union("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1", "2", "3")

	got, err = s.Union("a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1", "2", "3", "9")
}

func unionMissing(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1")
	got, err := s.Union("a", "ghost")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1")
}

func difference(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "universe", "1", "2", "3", "4")
	mustAdd(t, s, "bs", "1", "3")
	mustAdd(t, s, "cs", "3", "4")

	got, err := s.Difference("universe", "bs")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "2", "4")

	got, err = s.Difference("universe", "bs", "cs")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "2")

	// Difference is not commutative.
	got, err = s.Difference("bs", "universe")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got)
}

func differenceNoKeys(t *testing.T, s ports.SetStore) {
	if _, err := s.Difference(); err == nil {
		t.Fatal("expected an error for zero keys")
	}
}

func unionStore(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2")
	mustAdd(t, s, "b", "2", "3")

	if err := s.UnionStore("dst", "a", "b"); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "dst", 3)

	// Overwrite, not append.
	if err := s.UnionStore("dst", "a"); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "dst", 2)

	// Union of nothing leaves dst empty (Redis deletes the key; mem keeps an
	// empty set; both read as cardinality 0).
	if err := s.UnionStore("dst", "ghost"); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "dst", 0)
}

func deleteKeys(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1")
	mustAdd(t, s, "b", "2")

	if err := s.Delete("a", "never-existed"); err != nil {
		t.Fatalf("Delete should tolerate missing keys: %v", err)
	}
	assertCard(t, s, "a", 0)
	assertCard(t, s, "b", 1) // untouched

	if err := s.Delete(); err != nil {
		t.Fatalf("Delete with no keys should be a no-op: %v", err)
	}
}

func docRoundTrip(t *testing.T, s ports.SetStore) {
	want := []byte(`{"major":"cs","credits":120}`)
	if err := s.PutDocument("plan", "1", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDocument("plan", "1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %s, want %s", got, want)
	}

	// Put again replaces.
	if err := s.PutDocument("plan", "1", []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDocument("plan", "1")
	if string(got) != `{"v":2}` {
		t.Fatalf("overwrite failed, got %s", got)
	}
}

func docMissingAndIsolation(t *testing.T, s ports.SetStore) {
	if _, err := s.GetDocument("plan", "404"); err == nil {
		t.Fatal("expected an error for a missing document")
	}

	if err := s.PutDocument("plan", "1", []byte(`"plan-one"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDocument("course", "1"); err == nil {
		t.Fatal("same id under a different entity must not collide")
	}
}

func setLaws(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2", "3", "4")
	mustAdd(t, s, "b", "3", "4", "5")

	i, _ := s.Intersect("a", "b")
	u, _ := s.Union("a", "b")
	aMinusB, _ := s.Difference("a", "b")
	bMinusA, _ := s.Difference("b", "a")

	// Inclusion-exclusion: |A ∪ B| = |A| + |B| − |A ∩ B|
	if len(u) != 4+3-len(i) {
		t.Fatalf("inclusion-exclusion violated: |∪|=%d |∩|=%d", len(u), len(i))
	}
	// A ∪ B = (A \ B) ∪ (A ∩ B) ∪ (B \ A), and the three parts are disjoint.
	if len(aMinusB)+len(i)+len(bMinusA) != len(u) {
		t.Fatalf("partition of A∪B violated: %d + %d + %d != %d",
			len(aMinusB), len(i), len(bMinusA), len(u))
	}
	// Jaccard = |A ∩ B| / |A ∪ B|
	if got, want := float64(len(i))/float64(len(u)), 0.4; got != want {
		t.Fatalf("jaccard = %v, want %v", got, want)
	}
}

func concurrentAdds(t *testing.T, s ports.SetStore) {
	const workers, perWorker = 8, 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := s.AddToSet("shared", string(rune('A'+w))+"-"+strconv.Itoa(i)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	assertCard(t, s, "shared", workers*perWorker)
}
