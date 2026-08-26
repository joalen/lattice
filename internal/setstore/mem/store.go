package mem

import (
	"sync"

	"github.com/joalen/lattice/internal/ports"
)

// Store is an in-memory implementation of the ports.SetStore, which backs
// plain Go maps. No need for network, persistence, this exists purely to let
// search/ingest/facets logic be developed and tested without Redis ever running.
type Store struct {
	mu   sync.RWMutex
	sets map[string]map[string]struct{} // setKey = set of members
	docs map[string][]byte              // "entity:id" = raw document bytes
}

// New returns a ready-to-use empty Store
func New() *Store {
	return &Store{
		sets: make(map[string]map[string]struct{}),
		docs: make(map[string][]byte),
	}
}

var _ ports.SetStore = (*Store)(nil)

// AddToSet ingests an initial string and one or more strings after and create out a key-value pairing
func (s *Store) AddToSet(setKey string, members ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sets[setKey] == nil {
		s.sets[setKey] = make(map[string]struct{})
	}

	for _, m := range members {
		s.sets[setKey][m] = struct{}{}
	}

	return nil
}
