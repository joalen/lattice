package search

import (
	"fmt"

	"github.com/joalen/lattice/internal/ports"
)

type Query struct {
	Facets []FacetMatch
}

type FacetMatch struct {
	Prefix string
	Values []string
}

func Find(store ports.SetStore, q Query) ([]string, error) {
	if len(q.Facets) == 0 {
		return nil, fmt.Errorf("search: query must have at least once facet")
	}

	keysToIntersect := make([]string, 0, len(q.Facets))
	tempCount := 0

	for _, facet := range q.Facets {
		if len(facet.Values) == 0 {
			continue
		}

		if len(facet.Values) == 1 {
			keysToIntersect = append(keysToIntersect, facet.Prefix+facet.Values[0])
		}

		setKeys := make([]string, len(facet.Values))
		for i, v := range facet.Values {
			setKeys[i] = facet.Prefix + v
		}

		union, err := store.Union(setKeys...)

		if err != nil {
			return nil, fmt.Errorf("search: union for facet %q: %w", facet.Prefix, err)
		}

		tempCount++
		tempKey := fmt.Sprintf("tmp:query:%d", tempCount)

		if err := store.AddToSet(tempKey, union...); err != nil {
			return nil, fmt.Errorf("search: materializing temp union: %w", err)
		}

		keysToIntersect = append(keysToIntersect, tempKey)
	}

	return store.Intersect(keysToIntersect...)
}
