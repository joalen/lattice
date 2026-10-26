package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type IndexType string

const (
	IndexSet   IndexType = "set"
	IndexRange IndexType = "range"
	IndexSkip  IndexType = "skip"
)

type Mapping struct {
	Entity string               `json:"entity"`
	ID     string               `json:"id"`
	Fields map[string]IndexType `json:"fields"`
}

func (m Mapping) Validate() error {
	if m.Entity == "" || strings.HasPrefix(m.Entity, "_") || strings.ContainsAny(m.Entity, ": \t\n") {
		return fmt.Errorf("mapping: entity %q must be non-empty, not start with an underscore, and contain no colons or whitespace", m.Entity)
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

func ParseMapping(r io.Reader) (Mapping, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var m Mapping
	if err := dec.Decode(&m); err != nil {
		return Mapping{}, fmt.Errorf("mapping: %w", err)
	}
	return m, m.Validate()
}

func (m Mapping) JSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
