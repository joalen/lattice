package search

import (
	"fmt"
	"sync/atomic"

	"github.com/joalen/lattice/internal/ports"
)

type Query struct {
	Facets []FacetMatch
}

type FacetMatch struct {
	Prefix string
	Values []string
}

var queryCounter atomic.Uint64

// Find returns result via set theory operations
func Find(store ports.SetStore, q Query) ([]string, error) {
	var keys, temps []string
	defer func() { _ = store.Delete(temps...) }()

	for _, facet := range q.Facets {
		switch len(facet.Values) {
		case 0:
			continue
		case 1:
			keys = append(keys, facet.Prefix+facet.Values[0])
		default:
			setKeys := make([]string, len(facet.Values))
			for i, v := range facet.Values {
				setKeys[i] = facet.Prefix + v
			}
			tmp := fmt.Sprintf("tmp:query:%d", queryCounter.Add(1))
			if err := store.UnionStore(tmp, setKeys...); err != nil {
				return nil, fmt.Errorf("search: union for facet %q: %w", facet.Prefix, err)
			}
			temps = append(temps, tmp)
			keys = append(keys, tmp)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("search: query must have at least one facet")
	}
	return store.Intersect(keys...)
}
