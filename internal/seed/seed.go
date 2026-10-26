// Package seed generates deterministic synthetic catalogs for tests and benchmarks.
//
// The shape is domain-neutral but exercises every index type: low and mid
// cardinality categorical fields, two numeric ranges, a multi-valued tag array
// and a nested object.
package seed

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/joalen/lattice/internal/ingest"
)

const Entity = "item"

type Owner struct {
	Team string `json:"team"`
	Name string `json:"name"`
}

type Item struct {
	ID     int      `json:"id"`
	Title  string   `json:"title"`
	Kind   string   `json:"kind"`
	Group  string   `json:"group"`
	Region string   `json:"region"`
	Tier   string   `json:"tier"`
	Year   int      `json:"year"`
	Score  int      `json:"score"`
	Tags   []string `json:"tags"`
	Owner  Owner    `json:"owner"`
}

const (
	numKinds   = 6
	numGroups  = 40
	numRegions = 12
	numTeams   = 25
	numTags    = 500
	MinYear    = 2010
	MaxYear    = 2026
	MaxScore   = 999
)

// Items returns n items with IDs 1..n. The same n and seed always give the same items.
func Items(n int, seed int64) []Item {
	rng := rand.New(rand.NewSource(seed))
	groupZipf := rand.NewZipf(rng, 1.2, 1, numGroups-1)
	tagZipf := rand.NewZipf(rng, 1.1, 1, numTags-1)

	items := make([]Item, n)
	for i := range items {
		id := i + 1
		tags := make([]string, 0, 8)
		seen := make(map[uint64]struct{}, 8)
		for want := 3 + rng.Intn(6); len(tags) < want; {
			t := tagZipf.Uint64()
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			tags = append(tags, fmt.Sprintf("tag-%03d", t))
		}

		tier := "free"
		switch r := rng.Float64(); {
		case r >= 0.9:
			tier = "enterprise"
		case r >= 0.6:
			tier = "pro"
		}

		items[i] = Item{
			ID:     id,
			Title:  fmt.Sprintf("item %d", id),
			Kind:   fmt.Sprintf("kind-%d", rng.Intn(numKinds)),
			Group:  fmt.Sprintf("group-%02d", groupZipf.Uint64()),
			Region: fmt.Sprintf("region-%02d", rng.Intn(numRegions)),
			Tier:   tier,
			Year:   MinYear + rng.Intn(MaxYear-MinYear+1),
			Score:  rng.Intn(MaxScore + 1),
			Tags:   tags,
			Owner:  Owner{Team: fmt.Sprintf("team-%02d", rng.Intn(numTeams)), Name: fmt.Sprintf("person-%d", id)},
		}
	}
	return items
}

func JSON(items []Item) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, it := range items {
		raw, err := json.Marshal(it)
		if err != nil {
			panic(err) // Item contains only strings and ints
		}
		out[i] = raw
	}
	return out
}

// Mapping is the index mapping for Items, written out rather than proposed so
// benchmarks do not depend on the discovery heuristics.
func Mapping() ingest.Mapping {
	return ingest.Mapping{
		Entity: Entity,
		ID:     "id",
		Fields: map[string]ingest.IndexType{
			"id":         ingest.IndexSkip,
			"title":      ingest.IndexSkip,
			"owner.name": ingest.IndexSkip,
			"kind":       ingest.IndexSet,
			"group":      ingest.IndexSet,
			"region":     ingest.IndexSet,
			"tier":       ingest.IndexSet,
			"tags":       ingest.IndexSet,
			"owner.team": ingest.IndexSet,
			"year":       ingest.IndexRange,
			"score":      ingest.IndexRange,
		},
	}
}
