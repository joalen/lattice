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

	s.addLocked(setKey, members)
	return nil
}

// Members returns every element of a set (SMEMBERS). A missing key is just the empty set.
func (s *Store) Members(setKey string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return keys(s.sets[setKey]), nil
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

// Difference does an intersect between two setKeys with one setKey becoming the complement (essentially difference from set theory)
func (s *Store) Difference(setKeys ...string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(setKeys) == 0 {
		return nil, fmt.Errorf("difference: no set keys given")
	}

	result := s.copySet(setKeys[0])
	for _, key := range setKeys[1:] {
		for member := range s.sets[key] {
			delete(result, member) // present in the "subtract" set? vanish
		}
	}

	return keys(result), nil
}

// UnionStore materializes the union of setKeys into dest, replacing whatever dest held (SUNIONSTORE)
func (s *Store) UnionStore(dest string, setKeys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]struct{})
	for _, key := range setKeys {
		for m := range s.sets[key] {
			out[m] = struct{}{}
		}
	}
	s.sets[dest] = out // replace, not append, which is SUNIONSTORE semantics
	return nil
}

// Delete removes whole sets; missing keys are ignored (DEL)
func (s *Store) Delete(keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, k := range keys {
		delete(s.sets, k)
	}
	return nil
}

// Apply runs ops in order under a single lock, so no reader ever sees a
// half-applied batch. Validating everything up front means an invalid op
// rejects the whole batch before anything is touched, the same all-or-nothing
// behavior MULTI/EXEC gives the Redis adapter.
func (s *Store) Apply(ops ...ports.Op) error {
	for _, op := range ops {
		if err := op.Validate(); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, op := range ops {
		switch op.Kind {
		case ports.OpAdd:
			s.addLocked(op.Key, op.Members)
		case ports.OpRemove:
			s.removeLocked(op.Key, op.Members)
		case ports.OpDelete:
			delete(s.sets, op.Key)
		case ports.OpPutDoc:
			s.docs[op.Entity+":"+op.ID] = append([]byte(nil), op.Raw...)
		case ports.OpDelDoc:
			delete(s.docs, op.Entity+":"+op.ID)
		}
	}
	return nil
}

func (s *Store) PutDocument(entity, id string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.docs[entity+":"+id] = append([]byte(nil), raw...) // copy: don't alias the caller's slice
	return nil
}

func (s *Store) GetDocument(entity, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, ok := s.docs[entity+":"+id]
	if !ok {
		return nil, fmt.Errorf("%w: document %s:%s", ports.ErrNotFound, entity, id)
	}

	return append([]byte(nil), raw...), nil
}

// addLocked and removeLocked assume the caller already holds s.mu.
func (s *Store) addLocked(setKey string, members []string) {
	if s.sets[setKey] == nil {
		s.sets[setKey] = make(map[string]struct{})
	}

	for _, m := range members {
		s.sets[setKey][m] = struct{}{}
	}
}

func (s *Store) removeLocked(setKey string, members []string) {
	set := s.sets[setKey]
	for _, m := range members {
		delete(set, m)
	}
	if len(set) == 0 {
		delete(s.sets, setKey) // Redis drops a set when it empties; behave the same
	}
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
