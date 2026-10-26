// Package contract is a behavior suite every ports.SetStore adapter must pass.
package contract

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/joalen/lattice/internal/ports"
)

// Factory returns an isolated, empty store.
type Factory func(t *testing.T) ports.SetStore

func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		fn   func(t *testing.T, s ports.SetStore)
	}{
		{"AddToSet is idempotent", addIdempotent},
		{"AddToSet with no members is a no-op", addNoMembers},
		{"Cardinality of a missing key is zero", cardinalityMissing},
		{"Members", members},
		{"MembersMulti", membersMulti},
		{"Batches larger than one pipeline", largeBatches},
		{"Intersect", intersect},
		{"Intersect with a missing key is empty", intersectMissing},
		{"Intersect with no keys errors", intersectNoKeys},
		{"Union", union},
		{"Union ignores missing keys", unionMissing},
		{"Difference", difference},
		{"Difference with no keys errors", differenceNoKeys},
		{"UnionStore materializes and overwrites", unionStore},
		{"IntersectStore materializes and overwrites", intersectStore},
		{"IntersectCard", intersectCard},
		{"IntersectCards", intersectCards},
		{"Delete removes sets and tolerates missing keys", deleteKeys},
		{"Keys by prefix", keysByPrefix},
		{"Sorted scores and ranges", sortedScores},
		{"Apply with scores", applyScores},
		{"Apply runs ops in order", applyInOrder},
		{"Apply with documents and empty ops", applyDocsAndEmpty},
		{"Apply rejects an invalid op without applying any", applyInvalid},
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

// Redis rejects SADD with no members, so the adapter has to guard it.
func addNoMembers(t *testing.T, s ports.SetStore) {
	if err := s.AddToSet("a"); err != nil {
		t.Fatalf("AddToSet with no members should not error: %v", err)
	}
	assertCard(t, s, "a", 0)
}

func cardinalityMissing(t *testing.T, s ports.SetStore) {
	assertCard(t, s, "nope", 0)
}

func members(t *testing.T, s ports.SetStore) {
	got, err := s.Members("ghost")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got)

	mustAdd(t, s, "a", "1", "2", "2", "3")
	got, err = s.Members("a")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1", "2", "3")
}

// Add then Remove on one key nets to a removal; ingest relies on this when moving a document between facets.
func membersMulti(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2")
	mustAdd(t, s, "b", "3")

	got, err := s.MembersMulti("a", "b", "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got keys %v, want a, b and ghost", got)
	}
	assertMembers(t, got["a"], "1", "2")
	assertMembers(t, got["b"], "3")
	assertMembers(t, got["ghost"])

	if got, err := s.MembersMulti(); err != nil || len(got) != 0 {
		t.Fatalf("no keys: %v, %v", got, err)
	}
}

func largeBatches(t *testing.T, s ports.SetStore) {
	const n = 2500
	setKeys := make([]string, n)
	members := make([]string, n)
	ops := make([]ports.Op, 0, 2*n)
	for i := 0; i < n; i++ {
		setKeys[i] = "set" + strconv.Itoa(i)
		members[i] = "m" + strconv.Itoa(i)
		ops = append(ops, ports.Add(setKeys[i], members[i]), ports.SetScore("z", members[i], float64(i)))
	}
	if err := s.Apply(ops...); err != nil {
		t.Fatal(err)
	}

	got, err := s.MembersMulti(setKeys...)
	if err != nil {
		t.Fatal(err)
	}
	for i, k := range setKeys {
		if len(got[k]) != 1 || got[k][0] != members[i] {
			t.Fatalf("MembersMulti[%s] = %v, want [%s]", k, got[k], members[i])
		}
	}

	if err := s.AddToSet("all", members...); err != nil {
		t.Fatal(err)
	}
	cards, err := s.IntersectCards("all", setKeys...)
	if err != nil || len(cards) != n {
		t.Fatalf("IntersectCards: %d results, %v", len(cards), err)
	}
	for i, c := range cards {
		if c != 1 {
			t.Fatalf("IntersectCards[%d] = %d, want 1", i, c)
		}
	}

	scores, err := s.Scores("z", append(members, "nobody")...)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != n || scores["m0"] != 0 || scores["m2499"] != 2499 {
		t.Fatalf("Scores returned %d entries, m0=%v m2499=%v", len(scores), scores["m0"], scores["m2499"])
	}
}

func applyInOrder(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "stale", "1", "2")

	err := s.Apply(
		ports.Add("fresh", "1", "2", "3"),
		ports.Remove("fresh", "3"),
		ports.Remove("stale", "1"),
		ports.Add("stale", "9"),
	)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.Members("fresh")
	assertMembers(t, got, "1", "2")
	got, _ = s.Members("stale")
	assertMembers(t, got, "2", "9")

	if err := s.Apply(ports.Remove("fresh", "1", "2")); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "fresh", 0)

	if err := s.Apply(ports.DeleteKey("stale")); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "stale", 0)
}

func applyDocsAndEmpty(t *testing.T, s ports.SetStore) {
	if err := s.Apply(); err != nil {
		t.Fatalf("Apply with no ops should be a no-op: %v", err)
	}
	if err := s.Apply(ports.Add("a"), ports.Remove("a")); err != nil {
		t.Fatalf("ops with empty member lists should be no-ops: %v", err)
	}

	if err := s.Apply(ports.PutDoc("plan", "1", []byte(`{"x":1}`)), ports.Add("idx", "1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDocument("plan", "1")
	if err != nil || string(got) != `{"x":1}` {
		t.Fatalf("PutDoc via Apply: got %q, %v", got, err)
	}
	assertCard(t, s, "idx", 1)

	if err := s.Apply(ports.DelDoc("plan", "1"), ports.DelDoc("plan", "never-existed")); err != nil {
		t.Fatalf("DelDoc should tolerate missing documents: %v", err)
	}
	if _, err := s.GetDocument("plan", "1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("after DelDoc: got %v, want ErrNotFound", err)
	}
}

func applyInvalid(t *testing.T, s ports.SetStore) {
	err := s.Apply(
		ports.Add("x", "1"),
		ports.Op{},
	)
	if err == nil {
		t.Fatal("expected an error for an invalid op")
	}
	assertCard(t, s, "x", 0)
}

func intersectStore(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2", "3")
	mustAdd(t, s, "b", "2", "3", "4")

	if err := s.IntersectStore("dst", "a", "b"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Members("dst")
	assertMembers(t, got, "2", "3")

	if err := s.IntersectStore("dst", "a", "ghost"); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "dst", 0)

	if err := s.IntersectStore("dst"); err == nil {
		t.Fatal("expected an error for zero keys")
	}
}

func intersectCard(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2", "3")
	mustAdd(t, s, "b", "2", "3", "4")

	for _, tc := range []struct {
		keys []string
		want int
	}{
		{[]string{"a", "b"}, 2},
		{[]string{"a"}, 3},
		{[]string{"a", "ghost"}, 0},
	} {
		n, err := s.IntersectCard(tc.keys...)
		if err != nil || n != tc.want {
			t.Fatalf("IntersectCard(%v) = %d, %v; want %d", tc.keys, n, err, tc.want)
		}
	}
	if _, err := s.IntersectCard(); err == nil {
		t.Fatal("expected an error for zero keys")
	}
}

func intersectCards(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "base", "1", "2", "3", "4")
	mustAdd(t, s, "a", "1", "2", "9")
	mustAdd(t, s, "b", "4")

	got, err := s.IntersectCards("base", "a", "b", "ghost", "base")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{2, 1, 0, 4}) {
		t.Fatalf("got %v, want [2 1 0 4]", got)
	}
	if got, err := s.IntersectCards("base"); err != nil || len(got) != 0 {
		t.Fatalf("no keys: %v, %v", got, err)
	}
	if got, _ := s.IntersectCards("ghost", "a"); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("missing base: %v", got)
	}
}

func keysByPrefix(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "p:role:admin", "1")
	mustAdd(t, s, "p:role:student", "2")
	mustAdd(t, s, "p:city:x", "1")
	mustAdd(t, s, "x*y:1", "1")
	mustAdd(t, s, "xzzy:1", "1")
	if err := s.Apply(ports.SetScore("p:_range:age", "1", 30)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDocument("p", "role:admin", []byte("{}")); err != nil {
		t.Fatal(err)
	}

	got, err := s.Keys("p:role:")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "p:role:admin", "p:role:student")

	got, _ = s.Keys("p:")
	assertMembers(t, got, "p:role:admin", "p:role:student", "p:city:x", "p:_range:age")

	// Glob characters in the prefix are literal.
	got, _ = s.Keys("x*y:")
	assertMembers(t, got, "x*y:1")

	got, _ = s.Keys("nothing:")
	assertMembers(t, got)
}

func sortedScores(t *testing.T, s ports.SetStore) {
	err := s.Apply(
		ports.SetScore("age", "a", 30),
		ports.SetScore("age", "b", 20),
		ports.SetScore("age", "c", 20),
		ports.SetScore("age", "d", 45.5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ordered := func(min, max float64) []string {
		t.Helper()
		got, err := s.RangeByScore("age", min, max)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := ordered(math.Inf(-1), math.Inf(1)); !reflect.DeepEqual(got, []string{"b", "c", "a", "d"}) {
		t.Fatalf("full range = %v, want ascending by score then member", got)
	}
	if got := ordered(20, 30); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("[20,30] = %v (bounds are inclusive)", got)
	}
	if got := ordered(31, math.Inf(1)); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("[31,inf) = %v", got)
	}
	if got := ordered(100, 200); len(got) != 0 {
		t.Fatalf("empty range = %v", got)
	}
	if got, _ := s.RangeByScore("ghost", math.Inf(-1), math.Inf(1)); len(got) != 0 {
		t.Fatalf("missing key = %v", got)
	}

	scores, err := s.Scores("age", "a", "d", "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scores, map[string]float64{"a": 30, "d": 45.5}) {
		t.Fatalf("Scores = %v", scores)
	}
	if got, err := s.Scores("age"); err != nil || len(got) != 0 {
		t.Fatalf("Scores with no members = %v, %v", got, err)
	}

	// Setting again replaces the score.
	if err := s.Apply(ports.SetScore("age", "a", 5)); err != nil {
		t.Fatal(err)
	}
	if got := ordered(math.Inf(-1), 10); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("after rescoring = %v", got)
	}

	if err := s.Apply(ports.DelScore("age", "a"), ports.DelScore("age", "never")); err != nil {
		t.Fatal(err)
	}
	if got := ordered(math.Inf(-1), math.Inf(1)); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Fatalf("after DelScore = %v", got)
	}

	if err := s.Delete("age"); err != nil {
		t.Fatal(err)
	}
	if got := ordered(math.Inf(-1), math.Inf(1)); len(got) != 0 {
		t.Fatalf("after Delete = %v", got)
	}
}

func applyScores(t *testing.T, s ports.SetStore) {
	err := s.Apply(
		ports.SetScore("z", "1", 1),
		ports.Op{Kind: ports.OpSetScore, Key: "z"},
	)
	if err == nil {
		t.Fatal("expected an error for a score op without a member")
	}
	if got, _ := s.RangeByScore("z", math.Inf(-1), math.Inf(1)); len(got) != 0 {
		t.Fatalf("nothing should have been applied, got %v", got)
	}
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

	got, err = s.Intersect("a")
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, got, "1", "2", "3")
}

func intersectMissing(t *testing.T, s ports.SetStore) {
	mustAdd(t, s, "a", "1", "2")

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

	// dst is replaced, not appended to.
	if err := s.UnionStore("dst", "a"); err != nil {
		t.Fatal(err)
	}
	assertCard(t, s, "dst", 2)

	// Redis deletes an emptied key, mem keeps an empty set; both read as 0.
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
	assertCard(t, s, "b", 1)

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

	if err := s.PutDocument("plan", "1", []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDocument("plan", "1")
	if string(got) != `{"v":2}` {
		t.Fatalf("overwrite failed, got %s", got)
	}
}

func docMissingAndIsolation(t *testing.T, s ports.SetStore) {
	_, err := s.GetDocument("plan", "404")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("missing document: got %v, want an error wrapping ports.ErrNotFound", err)
	}

	if err := s.PutDocument("plan", "1", []byte(`"plan-one"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDocument("course", "1"); !errors.Is(err, ports.ErrNotFound) {
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

	// |A ∪ B| = |A| + |B| − |A ∩ B|
	if len(u) != 4+3-len(i) {
		t.Fatalf("inclusion-exclusion violated: |∪|=%d |∩|=%d", len(u), len(i))
	}
	// A ∪ B splits into A\B, A ∩ B and B\A with no overlap.
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
