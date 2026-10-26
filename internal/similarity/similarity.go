package similarity

import (
	"context"
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
func Similar(ctx context.Context, store ports.SetStore, entity, id string, opts Options) ([]Match, error) {
	own, err := store.Members(ctx, keyspace.Memberships(entity, id))
	if err != nil {
		return nil, fmt.Errorf("similarity: %w", err)
	}
	if len(own) == 0 {
		return nil, fmt.Errorf("similarity: document %q: %w", id, ports.ErrNotFound)
	}
	target := facetSet(entity, own, opts.Paths)

	targetKeys := make([]string, 0, len(target))
	for k := range target {
		targetKeys = append(targetKeys, k)
	}
	lists, err := store.MembersMulti(ctx, targetKeys...)
	if err != nil {
		return nil, fmt.Errorf("similarity: %w", err)
	}
	shared := make(map[string]int)
	for _, members := range lists {
		for _, other := range members {
			if other != id {
				shared[other]++
			}
		}
	}

	candidates := make([]string, 0, len(shared))
	memberKeys := make([]string, 0, len(shared))
	for other := range shared {
		candidates = append(candidates, other)
		memberKeys = append(memberKeys, keyspace.Memberships(entity, other))
	}
	theirs, err := store.MembersMulti(ctx, memberKeys...)
	if err != nil {
		return nil, fmt.Errorf("similarity: %w", err)
	}

	matches := make([]Match, len(candidates))
	for i, other := range candidates {
		score := Jaccard(target, facetSet(entity, theirs[memberKeys[i]], opts.Paths))
		matches[i] = Match{ID: other, Score: score, Shared: shared[other]}
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

func facetSet(entity string, keys []string, paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if keyspace.IsFacet(entity, k) && inPaths(entity, k, paths) {
			out[k] = struct{}{}
		}
	}
	return out
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
