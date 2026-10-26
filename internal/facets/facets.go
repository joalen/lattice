package facets

import (
	"crypto/rand"
	"fmt"
	"sort"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

// Count is how many of the given documents have Value. Value is the normalized
// form stored in the key, so "Computer Science" comes back as "computer_science".
type Count struct {
	Value string `json:"value"`
	N     int    `json:"count"`
}

// Counts reports, for each path, the values present among ids and how many of
// ids carry each. Values with a zero count are omitted; results are ordered by
// count descending, then value. Round trips stay constant however many values
// a field has.
func Counts(store ports.SetStore, entity string, paths []string, ids []string) (map[string][]Count, error) {
	out := make(map[string][]Count, len(paths))
	if len(ids) == 0 || len(paths) == 0 {
		return out, nil
	}

	valueSets := make([]string, len(paths))
	for i, p := range paths {
		valueSets[i] = keyspace.Values(entity, p)
	}
	known, err := store.MembersMulti(valueSets...)
	if err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}

	type candidate struct{ path, value string }
	var cands []candidate
	var keys []string
	for i, p := range paths {
		for _, v := range known[valueSets[i]] {
			cands = append(cands, candidate{p, v})
			keys = append(keys, keyspace.Facet(entity, p, v))
		}
	}
	if len(keys) == 0 {
		return out, nil
	}

	current := entity + ":_tmp:" + rand.Text()
	defer func() { _ = store.Delete(current) }()
	if err := store.AddToSet(current, ids...); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	cards, err := store.IntersectCards(current, keys...)
	if err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}

	for i, c := range cands {
		if cards[i] > 0 {
			out[c.path] = append(out[c.path], Count{Value: c.value, N: cards[i]})
		}
	}
	for _, counts := range out {
		sort.Slice(counts, func(i, j int) bool {
			if counts[i].N != counts[j].N {
				return counts[i].N > counts[j].N
			}
			return counts[i].Value < counts[j].Value
		})
	}
	return out, nil
}
