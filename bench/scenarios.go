package main

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strconv"
	"time"

	"github.com/joalen/lattice/internal/facets"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/seed"
	"github.com/joalen/lattice/internal/similarity"
)

const warmup = 10

func newRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// Value pools matching what internal/seed generates.
var (
	kinds   = pool("kind-%d", 6)
	groups  = pool("group-%02d", 40)
	regions = pool("region-%02d", 12)
	tiers   = []string{"free", "pro", "enterprise"}
)

func pool(format string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(format, i)
	}
	return out
}

// pickMany returns k distinct values from pool, starting with must.
func pickMany(rng *rand.Rand, pool []string, must string, k int) []string {
	out := []string{must}
	for len(out) < k {
		if v := pool[rng.Intn(len(pool))]; !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

type scenario struct {
	name string
	// run returns how many documents matched, for context next to the timings.
	run func(rng *rand.Rand) (int, error)
}

func scenarios(ctx context.Context, store ports.SetStore, items []seed.Item) []scenario {
	pick := func(rng *rand.Rand) seed.Item { return items[rng.Intn(len(items))] }
	facet := func(path string, vals ...string) search.Facet { return search.Facet{Path: path, Values: vals} }
	find := func(q search.Query) (int, error) {
		q.Entity = seed.Entity
		res, err := search.Find(ctx, store, q)
		return res.Total, err
	}
	yearRange := func(it seed.Item, spread int) search.Range {
		lo, hi := float64(it.Year-spread), float64(it.Year+spread)
		return search.Range{Path: "year", Min: &lo, Max: &hi}
	}
	byScore := &search.Sort{Path: "score", Desc: true}
	deepOffset := max(len(items)/20, 10)

	counts := func(q search.Query) (int, error) {
		q.Entity = seed.Entity
		ids, err := search.Match(ctx, store, q)
		if err != nil {
			return 0, err
		}
		_, err = facets.Counts(ctx, store, seed.Entity, []string{"kind", "tier", "region", "owner.team"}, ids)
		return len(ids), err
	}

	return []scenario{
		{"get(id)", func(rng *rand.Rand) (int, error) {
			_, err := store.GetDocument(ctx, seed.Entity, strconv.Itoa(pick(rng).ID))
			return 1, err
		}},
		{"1 facet, page of 20", func(rng *rand.Rand) (int, error) {
			return find(search.Query{Facets: []search.Facet{facet("group", pick(rng).Group)}, Limit: 20})
		}},
		{"3 facets (OR lists) + range, sorted, page of 20  [PRD target]", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return find(search.Query{
				Facets: []search.Facet{
					facet("kind", pickMany(rng, kinds, it.Kind, 2)...),
					facet("tier", pickMany(rng, tiers, it.Tier, 2)...),
					facet("region", pickMany(rng, regions, it.Region, 3)...),
				},
				Ranges: []search.Range{yearRange(it, 5)},
				Sort:   byScore,
				Limit:  20,
			})
		}},
		{"OR within a facet AND another facet", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return find(search.Query{
				Facets: []search.Facet{facet("group", pickMany(rng, groups, it.Group, 3)...), facet("tier", it.Tier)},
				Limit:  20,
			})
		}},
		{"NOT: one facet minus another", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return find(search.Query{
				Facets: []search.Facet{facet("group", it.Group)},
				Not:    []search.Facet{facet("tier", it.Tier)},
				Limit:  20,
			})
		}},
		{"multi-valued field (tag) AND facet", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return find(search.Query{
				Facets: []search.Facet{facet("tags", it.Tags[0]), facet("kind", it.Kind)},
				Limit:  20,
			})
		}},
		{"broad filter, sorted, first page", func(rng *rand.Rand) (int, error) {
			return find(search.Query{Facets: []search.Facet{facet("tier", pick(rng).Tier)}, Sort: byScore, Limit: 20})
		}},
		{fmt.Sprintf("broad filter, sorted, deep page (offset %d)", deepOffset), func(rng *rand.Rand) (int, error) {
			return find(search.Query{Facets: []search.Facet{facet("tier", pick(rng).Tier)}, Sort: byScore, Offset: deepOffset, Limit: 20})
		}},
		{"facet counts over a narrow filter", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return counts(search.Query{Facets: []search.Facet{facet("group", it.Group), facet("region", it.Region), facet("tier", it.Tier)}})
		}},
		{"facet counts over a broad filter", func(rng *rand.Rand) (int, error) {
			it := pick(rng)
			return counts(search.Query{Facets: []search.Facet{facet("tier", it.Tier)}, Ranges: []search.Range{yearRange(it, 5)}})
		}},
		{"similar(id), top 10", func(rng *rand.Rand) (int, error) {
			m, err := similarity.Similar(ctx, store, seed.Entity, strconv.Itoa(pick(rng).ID), similarity.Options{Limit: 10})
			return len(m), err
		}},
	}
}

type opResult struct {
	Name          string
	Samples       int
	Matches       int
	P50, P95, P99 time.Duration
	Trips         int64
	Err           error
}

type runner struct {
	iters, minSamples int
	budget            time.Duration
	trips             *tripCounter
	seed              int64
}

// measure times fn until iters samples are taken or the budget runs out,
// but never fewer than minSamples.
func (r runner) measure(name string, fn func(rng *rand.Rand) (int, error)) opResult {
	rng := newRand(r.seed)
	for i := 0; i < warmup; i++ {
		if _, err := fn(rng); err != nil {
			return opResult{Name: name, Err: err}
		}
	}

	var durs []time.Duration
	var trips []int64
	var matches []int
	start := time.Now()
	for i := 0; i < r.iters; i++ {
		if i >= r.minSamples && time.Since(start) > r.budget {
			break
		}
		before := r.trips.now()
		t0 := time.Now()
		n, err := fn(rng)
		d := time.Since(t0)
		if err != nil {
			return opResult{Name: name, Err: err}
		}
		durs = append(durs, d)
		trips = append(trips, r.trips.now()-before)
		matches = append(matches, n)
	}

	slices.Sort(durs)
	return opResult{
		Name:    name,
		Samples: len(durs),
		Matches: median(matches),
		P50:     percentile(durs, 50),
		P95:     percentile(durs, 95),
		P99:     percentile(durs, 99),
		Trips:   median(trips),
	}
}
