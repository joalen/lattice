package similarity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

type Match struct {
	ID     string
	Score  float64
	Shared int
}

type Options struct {
	// Paths limits comparison to these fields; empty compares every facet.
	Paths []string
	Limit int // 0 means no limit
}

// Jaccard is |a ∩ b| / |a ∪ b|, and 0 when both sets are empty.
func Jaccard(a, b map[string]struct{}) float64 {
	small, large := a, b
	if len(small) > len(large) {
		small, large = large, small
	}
	shared := 0
	for k := range small {
		if _, ok := large[k]; ok {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// Similar ranks other documents by Jaccard similarity of their facet sets to id's.
func Similar(store ports.SetStore, entity, id string, opts Options) ([]Match, error) {
	target, err := features(store, entity, id, opts.Paths)
	if err != nil {
		return nil, err
	}

	shared := make(map[string]int)
	for key := range target {
		members, err := store.Members(key)
		if err != nil {
			return nil, fmt.Errorf("similarity: %w", err)
		}
		for _, other := range members {
			if other != id {
				shared[other]++
			}
		}
	}

	matches := make([]Match, 0, len(shared))
	for other, n := range shared {
		theirs, err := features(store, entity, other, opts.Paths)
		if err != nil {
			return nil, err
		}
		matches = append(matches, Match{ID: other, Score: Jaccard(target, theirs), Shared: n})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})
	if opts.Limit > 0 && opts.Limit < len(matches) {
		matches = matches[:opts.Limit]
	}
	return matches, nil
}

func features(store ports.SetStore, entity, id string, paths []string) (map[string]struct{}, error) {
	keys, err := store.Members(keyspace.Memberships(entity, id))
	if err != nil {
		return nil, fmt.Errorf("similarity: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("similarity: document %q: %w", id, ports.ErrNotFound)
	}

	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if keyspace.IsFacet(entity, k) && inPaths(entity, k, paths) {
			out[k] = struct{}{}
		}
	}
	return out, nil
}

func inPaths(entity, key string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		if strings.HasPrefix(key, keyspace.FacetPrefix(entity, p)) {
			return true
		}
	}
	return false
}
