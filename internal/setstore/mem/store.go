package mem

import (
	"fmt"
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

// Cardinality returns the count of all distinct elements in a setKey (from set theory)
func (s *Store) Cardinality(setKey string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.sets[setKey]), nil
}

// Intersect derives common setKey within setKeys (essentially intersection from set theory)
func (s *Store) Intersect(setKeys ...string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(setKeys) == 0 {
		return nil, fmt.Errorf("intersect: no set keys given")
	}

	result := s.copySet(setKeys[0])
	for _, key := range setKeys[1:] {
		for member := range result {
			if _, ok := s.sets[key][member]; !ok {
				delete(result, member) // not in set therefore drop from running intersection
			}
		}
	}

	return keys(result), nil
}

// Union aggregates setKeys within setKeys (essentially union from set theory)
func (s *Store) Union(setKeys ...string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]struct{})
	for _, key := range setKeys {
		for member := range s.sets[key] {
			result[member] = struct{}{}
		}
	}
	return keys(result), nil
}

func (s *Store) copySet(setKey string) map[string]struct{} {
	out := make(map[string]struct{})

	for member := range s.sets[setKey] {
		out[member] = struct{}{}
	}

	return out
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))

	for k := range m {
		out = append(out, k)
	}

	return out
}
