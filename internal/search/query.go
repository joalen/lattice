package search

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

// Facet matches documents whose field at Path has any of Values.
type Facet struct {
	Path   string
	Values []string
}

// Range matches documents whose numeric field is within [Min, Max]; nil is unbounded.
type Range struct {
	Path     string
	Min, Max *float64
}

type Sort struct {
	Path string
	Desc bool
}

// Query is OR within a Facet, AND across Facets and Ranges, minus anything matching Not.
type Query struct {
	Entity string
	Facets []Facet
	Not    []Facet
	Ranges []Range
	Sort   *Sort
	Offset int
	Limit  int // 0 means no limit
}

type Result struct {
	IDs   []string
	Total int
}

// Match returns every matching ID, unordered.
func Match(store ports.SetStore, q Query) ([]string, error) {
	if q.Entity == "" {
		return nil, errors.New("search: query needs an entity")
	}

	var temps []string
	defer func() { _ = store.Delete(temps...) }()
	temp := func() string {
		k := q.Entity + ":_tmp:" + rand.Text()
		temps = append(temps, k)
		return k
	}

	// An empty Values list contributes nothing; several values are unioned server-side.
	facetKey := func(f Facet) (string, bool, error) {
		keys := make([]string, len(f.Values))
		for i, v := range f.Values {
			keys[i] = keyspace.Facet(q.Entity, f.Path, v)
		}
		switch len(keys) {
		case 0:
			return "", false, nil
		case 1:
			return keys[0], true, nil
		}
		tmp := temp()
		if err := store.UnionStore(tmp, keys...); err != nil {
			return "", false, fmt.Errorf("search: union for %q: %w", f.Path, err)
		}
		return tmp, true, nil
	}

	var positive []string
	for _, f := range q.Facets {
		key, ok, err := facetKey(f)
		if err != nil {
			return nil, err
		}
		if ok {
			positive = append(positive, key)
		}
	}
	for _, r := range q.Ranges {
		lo, hi := math.Inf(-1), math.Inf(1)
		if r.Min != nil {
			lo = *r.Min
		}
		if r.Max != nil {
			hi = *r.Max
		}
		ids, err := store.RangeByScore(keyspace.Range(q.Entity, r.Path), lo, hi)
		if err != nil {
			return nil, fmt.Errorf("search: range %q: %w", r.Path, err)
		}
		tmp := temp()
		if err := store.AddToSet(tmp, ids...); err != nil {
			return nil, fmt.Errorf("search: range %q: %w", r.Path, err)
		}
		positive = append(positive, tmp)
	}
	if len(positive) == 0 {
		positive = []string{keyspace.Universe(q.Entity)}
	}

	var negative []string
	for _, f := range q.Not {
		key, ok, err := facetKey(f)
		if err != nil {
			return nil, err
		}
		if ok {
			negative = append(negative, key)
		}
	}
	if len(negative) == 0 {
		return store.Intersect(positive...)
	}

	matched := temp()
	if err := store.IntersectStore(matched, positive...); err != nil {
		return nil, fmt.Errorf("search: intersect: %w", err)
	}
	return store.Difference(append([]string{matched}, negative...)...)
}

// Find runs Match, then orders and pages the result. Without a Sort, IDs are
// ordered by string comparison so pages stay stable.
func Find(store ports.SetStore, q Query) (Result, error) {
	if q.Offset < 0 || q.Limit < 0 {
		return Result{}, errors.New("search: offset and limit must not be negative")
	}

	ids, err := Match(store, q)
	if err != nil {
		return Result{}, err
	}
	res := Result{Total: len(ids)}

	if q.Sort != nil {
		scores, err := store.Scores(keyspace.Range(q.Entity, q.Sort.Path), ids...)
		if err != nil {
			return Result{}, fmt.Errorf("search: sort %q: %w", q.Sort.Path, err)
		}
		sort.Slice(ids, func(i, j int) bool {
			a, aok := scores[ids[i]]
			b, bok := scores[ids[j]]
			switch {
			case aok && bok && a != b:
				return (a < b) != q.Sort.Desc
			case aok != bok:
				return aok
			}
			return ids[i] < ids[j]
		})
	} else {
		sort.Strings(ids)
	}

	if q.Offset >= len(ids) {
		res.IDs = []string{}
		return res, nil
	}
	ids = ids[q.Offset:]
	if q.Limit > 0 && q.Limit < len(ids) {
		ids = ids[:q.Limit]
	}
	res.IDs = ids
	return res, nil
}
