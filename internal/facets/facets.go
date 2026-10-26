package facets

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

// Count is how many of the given documents have Value. Value is the normalized
// form stored in the key, so "Computer Science" comes back as "computer_science".
type Count struct {
	Value string
	N     int
}

// Counts reports, for each path, the values present among ids and how many of
// ids carry each. Values with a zero count are omitted; results are ordered by
// count descending, then value.
func Counts(store ports.SetStore, entity string, paths []string, ids []string) (map[string][]Count, error) {
	out := make(map[string][]Count, len(paths))
	if len(ids) == 0 {
		return out, nil
	}

	current := entity + ":_tmp:" + rand.Text()
	defer func() { _ = store.Delete(current) }()
	if err := store.AddToSet(current, ids...); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}

	for _, path := range paths {
		prefix := keyspace.FacetPrefix(entity, path)
		keys, err := store.Keys(prefix)
		if err != nil {
			return nil, fmt.Errorf("facets: %q: %w", path, err)
		}

		var counts []Count
		for _, key := range keys {
			n, err := store.IntersectCard(current, key)
			if err != nil {
				return nil, fmt.Errorf("facets: %q: %w", key, err)
			}
			if n > 0 {
				counts = append(counts, Count{Value: strings.TrimPrefix(key, prefix), N: n})
			}
		}
		sort.Slice(counts, func(i, j int) bool {
			if counts[i].N != counts[j].N {
				return counts[i].N > counts[j].N
			}
			return counts[i].Value < counts[j].Value
		})
		if len(counts) > 0 {
			out[path] = counts
		}
	}
	return out, nil
}
