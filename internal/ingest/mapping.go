package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// IndexType says how one field path is indexed.
type IndexType string

const (
	IndexSet   IndexType = "set"   // categorical or array-of-primitives: one Redis SET per value
	IndexRange IndexType = "range" // numeric: a sorted set (builder support lands with range queries)
	IndexSkip  IndexType = "skip"  // considered and deliberately not indexed
)

// Mapping is the reviewable config between discovery and indexing: discovery
// proposes it, a human edits it once, and the builder obeys it!
type Mapping struct {
	Entity string               `json:"entity"` // key namespace, e.g. "plan"
	ID     string               `json:"id"`     // dot-path of the document ID
	Fields map[string]IndexType `json:"fields"` // dot-path -> how to index it
}

func (m Mapping) Validate() error {
	if m.Entity == "" || strings.ContainsAny(m.Entity, ": \t\n") {
		return fmt.Errorf("mapping: entity %q must be non-empty with no colons or whitespace", m.Entity)
	}
	if m.ID == "" {
		return fmt.Errorf("mapping: id path is required")
	}
	for path, typ := range m.Fields {
		switch typ {
		case IndexSet, IndexRange, IndexSkip:
		default:
			return fmt.Errorf("mapping: field %q has unknown index type %q (want set, range or skip)", path, typ)
		}
	}
	return nil
}

// ParseMapping reads a mapping and rejects unknown keys
func ParseMapping(r io.Reader) (Mapping, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var m Mapping
	if err := dec.Decode(&m); err != nil {
		return Mapping{}, fmt.Errorf("mapping: %w", err)
	}
	return m, m.Validate()
}

// JSON renders the mapping for review. encoding/json sorts map keys
func (m Mapping) JSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
