package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/joalen/lattice/internal/ports"
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

// metaEntity cannot collide with a real entity because Validate rejects names starting with "_".
const metaEntity = "_mapping"

// SaveMapping records the mapping an entity was indexed with, so queries can
// tell an unindexed field from a field with no matches.
func SaveMapping(store ports.SetStore, m Mapping) error {
	data, err := m.JSON()
	if err != nil {
		return err
	}
	return store.PutDocument(metaEntity, m.Entity, data)
}

// LoadMapping returns an error wrapping ports.ErrNotFound if the entity was never ingested.
func LoadMapping(store ports.SetStore, entity string) (Mapping, error) {
	raw, err := store.GetDocument(metaEntity, entity)
	if err != nil {
		return Mapping{}, fmt.Errorf("mapping for entity %q: %w", entity, err)
	}
	return ParseMapping(bytes.NewReader(raw))
}
