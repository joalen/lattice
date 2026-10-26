package mem

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/joalen/lattice/internal/ports"
)

// Store is an in-memory implementation of the ports.SetStore, which backs
// plain Go maps. No need for network, persistence, this exists purely to let
// search/ingest/facets logic be developed and tested without Redis ever running.
type Store struct {
	mu    sync.RWMutex
	sets  map[string]map[string]struct{} // setKey = set of members
	zsets map[string]map[string]float64  // key = member -> score
	docs  map[string][]byte              // "entity:id" = raw document bytes
}

// New returns a ready-to-use empty Store
func New() *Store {
	return &Store{
		sets:  make(map[string]map[string]struct{}),
		zsets: make(map[string]map[string]float64),
		docs:  make(map[string][]byte),
	}
}

var _ ports.SetStore = (*Store)(nil)

// AddToSet ingests an initial string and one or more strings after and create out a key-value pairing
func (s *Store) AddToSet(_ context.Context, setKey string, members ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.addLocked(setKey, members)
	return nil
}

func (s *Store) Members(_ context.Context, setKey string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return keys(s.sets[setKey]), nil
}

// Cardinality returns the count of all distinct elements in a setKey (from set theory)
func (s *Store) MembersMulti(_ context.Context, setKeys ...string) (map[string][]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string][]string, len(setKeys))
	for _, k := range setKeys {
		out[k] = keys(s.sets[k])
	}
	return out, nil
}

func (s *Store) Cardinality(_ context.Context, setKey string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.sets[setKey]), nil
}

// Intersect derives common setKey within setKeys (essentially intersection from set theory)
func (s *Store) Intersect(_ context.Context, setKeys ...string) ([]string, error) {
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
func (s *Store) Union(_ context.Context, setKeys ...string) ([]string, error) {
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
func (s *Store) Difference(_ context.Context, setKeys ...string) ([]string, error) {
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

func (s *Store) UnionStore(_ context.Context, dest string, setKeys ...string) error {
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

func (s *Store) IntersectStore(_ context.Context, dest string, setKeys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(setKeys) == 0 {
		return fmt.Errorf("intersect: no set keys given")
	}
	s.sets[dest] = s.intersectLocked(setKeys)
	return nil
}

func (s *Store) IntersectCard(_ context.Context, setKeys ...string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(setKeys) == 0 {
		return 0, fmt.Errorf("intersect: no set keys given")
	}
	return len(s.intersectLocked(setKeys)), nil
}

func (s *Store) IntersectCards(_ context.Context, base string, setKeys ...string) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]int, len(setKeys))
	for i, k := range setKeys {
		for m := range s.sets[base] {
			if _, ok := s.sets[k][m]; ok {
				out[i]++
			}
		}
	}
	return out, nil
}

func (s *Store) Delete(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, k := range keys {
		delete(s.sets, k)
		delete(s.zsets, k)
	}
	return nil
}

func (s *Store) Keys(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []string
	for k := range s.sets {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	for k := range s.zsets {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) RangeByScore(_ context.Context, key string, min, max float64) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []string
	for m, score := range s.zsets[key] {
		if score >= min && score <= max {
			out = append(out, m)
		}
	}
	z := s.zsets[key]
	sort.Slice(out, func(i, j int) bool {
		if z[out[i]] != z[out[j]] {
			return z[out[i]] < z[out[j]]
		}
		return out[i] < out[j]
	})
	return out, nil
}

func (s *Store) Scores(_ context.Context, key string, members ...string) (map[string]float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]float64, len(members))
	for _, m := range members {
		if score, ok := s.zsets[key][m]; ok {
			out[m] = score
		}
	}
	return out, nil
}

// Apply validates every op first, then applies them under one lock, so a bad op leaves the store untouched.
func (s *Store) Apply(_ context.Context, ops ...ports.Op) error {
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
			delete(s.zsets, op.Key)
		case ports.OpPutDoc:
			s.docs[op.Entity+":"+op.ID] = append([]byte(nil), op.Raw...)
		case ports.OpDelDoc:
			delete(s.docs, op.Entity+":"+op.ID)
		case ports.OpSetScore:
			if s.zsets[op.Key] == nil {
				s.zsets[op.Key] = make(map[string]float64)
			}
			s.zsets[op.Key][op.Member] = op.Score
		case ports.OpDelScore:
			delete(s.zsets[op.Key], op.Member)
			if len(s.zsets[op.Key]) == 0 {
				delete(s.zsets, op.Key)
			}
		}
	}
	return nil
}

func (s *Store) PutDocument(_ context.Context, entity, id string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.docs[entity+":"+id] = append([]byte(nil), raw...)
	return nil
}

func (s *Store) GetDocument(_ context.Context, entity, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, ok := s.docs[entity+":"+id]
	if !ok {
		return nil, fmt.Errorf("%w: document %s:%s", ports.ErrNotFound, entity, id)
	}

	return append([]byte(nil), raw...), nil
}

// addLocked, removeLocked and intersectLocked assume the caller holds s.mu.
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
		delete(s.sets, setKey) // Redis drops a set when it empties
	}
}

func (s *Store) intersectLocked(setKeys []string) map[string]struct{} {
	result := s.copySet(setKeys[0])
	for _, key := range setKeys[1:] {
		for member := range result {
			if _, ok := s.sets[key][member]; !ok {
				delete(result, member)
			}
		}
	}
	return result
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
