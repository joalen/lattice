package ingest

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ProposeOptions tunes the heuristics. Zero values pick sensible defaults.
type ProposeOptions struct {
	ID               string // dot-path of the ID field; empty means "guess"
	MaxCategorical   int    // max distinct values for a scalar string to count as a facet (default 30)
	MaxArrayDistinct int    // same limit for array-of-primitives fields (default 1000)
}

var idCandidates = []string{"id", "ID", "Id", "_id", "uuid", "key"}

type pathStats struct {
	present  int // documents that have this path
	array    bool
	kinds    map[ScalarKind]struct{}
	distinct map[string]struct{}
}

// ProposeMapping is the discovery pass: it flattens every document, profiles
// each path, and proposes how to index it.
func ProposeMapping(entity string, docs []json.RawMessage, opts ProposeOptions) (Mapping, error) {
	if len(docs) == 0 {
		return Mapping{}, fmt.Errorf("propose: no documents")
	}
	if opts.MaxCategorical == 0 {
		opts.MaxCategorical = 30
	}
	if opts.MaxArrayDistinct == 0 {
		opts.MaxArrayDistinct = 1000
	}

	flats := make([]map[string]Field, len(docs))
	stats := make(map[string]*pathStats)

	for i, raw := range docs {
		flat, err := Flatten(raw)
		if err != nil {
			return Mapping{}, fmt.Errorf("propose: document %d: %w", i, err)
		}
		flats[i] = flat

		for path, f := range flat {
			st := stats[path]
			if st == nil {
				st = &pathStats{kinds: map[ScalarKind]struct{}{}, distinct: map[string]struct{}{}}
				stats[path] = st
			}
			st.present++
			st.array = st.array || f.Array
			for _, v := range f.Values {
				st.kinds[v.Kind] = struct{}{}
				st.distinct[v.Text] = struct{}{}
			}
		}
	}

	idPath := opts.ID
	if idPath == "" {
		var err error
		if idPath, err = guessID(flats, stats); err != nil {
			return Mapping{}, err
		}
	}

	m := Mapping{Entity: entity, ID: idPath, Fields: make(map[string]IndexType, len(stats))}
	for path, st := range stats {
		m.Fields[path] = classify(path, idPath, st, opts)
	}
	return m, m.Validate()
}

func classify(path, idPath string, st *pathStats, opts ProposeOptions) IndexType {
	switch {
	case path == idPath:
		return IndexSkip // the ID identifies a document; it is not a facet
	case len(st.kinds) != 1:
		return IndexSkip // mixed types across documents: ambiguous, needs a human
	}

	var kind ScalarKind
	for k := range st.kinds {
		kind = k
	}

	switch {
	case kind == KindNumber && !st.array:
		return IndexRange
	case kind == KindNumber:
		return IndexSkip
	case st.array:
		if len(st.distinct) <= opts.MaxArrayDistinct {
			return IndexSet
		}
	case kind == KindBool:
		return IndexSet
	default:
		// A scalar string is a useful facet only if it is low-cardinality AND
		// its values actually repeat.
		if len(st.distinct) <= opts.MaxCategorical && len(st.distinct) < st.present {
			return IndexSet
		}
	}
	return IndexSkip
}

// guessID picks the first conventional ID path that is present exactly once
func guessID(flats []map[string]Field, stats map[string]*pathStats) (string, error) {
	for _, cand := range idCandidates {
		st, ok := stats[cand]
		if !ok || st.present != len(flats) || len(st.distinct) != len(flats) || st.array {
			continue
		}
		return cand, nil
	}
	paths := make([]string, 0, len(stats))
	for p, st := range stats {
		if st.present == len(flats) && len(st.distinct) == len(flats) && !st.array {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return "", fmt.Errorf("propose: could not guess an id field; set ProposeOptions.ID (unique in every document: %v)", paths)
}
