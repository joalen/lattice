package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/ports"
)

// Compile-time proof that *Store satisfies the port. If the interface grows
// and this adapter falls behind, the build breaks here rather than at runtime and
// emits message for review
var _ ports.SetStore = (*Store)(nil)

// Store implements ports.SetStore on top of Redis.
type Store struct {
	rdb    goredis.Cmdable
	prefix string
}

// Option configures a Store (the "functional options" pattern: optional
// settings without a constructor that grows a parameter per feature).
type Option func(*Store)

// WithPrefix namespaces every key this Store touches
func WithPrefix(p string) Option {
	return func(s *Store) { s.prefix = p }
}

func New(rdb goredis.Cmdable, opts ...Option) *Store {
	s := &Store{rdb: rdb}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// The port does not take a context yet, so the adapter supplies one.
func (s *Store) ctx() context.Context { return context.Background() }

func (s *Store) k(key string) string { return s.prefix + key }

func (s *Store) ks(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = s.k(key)
	}
	return out
}

// Documents and sets share one Redis keyspace, unlike mem.Store, which keeps
// them in separate maps.
func (s *Store) docKey(entity, id string) string {
	return s.prefix + "doc:" + entity + ":" + id
}

func (s *Store) AddToSet(setKey string, members ...string) error {
	if len(members) == 0 {
		return nil // SADD with no members is a Redis error; therefore no-op
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	if err := s.rdb.SAdd(s.ctx(), s.k(setKey), args...).Err(); err != nil {
		return fmt.Errorf("redis: SADD %q: %w", setKey, err)
	}
	return nil
}

func (s *Store) Intersect(setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return nil, errors.New("intersect: no set keys given")
	}
	out, err := s.rdb.SInter(s.ctx(), s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SINTER: %w", err)
	}
	return out, nil
}

func (s *Store) Union(setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return []string{}, nil // union of nothing is the empty set
	}
	out, err := s.rdb.SUnion(s.ctx(), s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SUNION: %w", err)
	}
	return out, nil
}

func (s *Store) Difference(setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return nil, errors.New("difference: no set keys given")
	}
	out, err := s.rdb.SDiff(s.ctx(), s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SDIFF: %w", err)
	}
	return out, nil
}

func (s *Store) Cardinality(setKey string) (int, error) {
	n, err := s.rdb.SCard(s.ctx(), s.k(setKey)).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: SCARD %q: %w", setKey, err)
	}
	return int(n), nil
}

// UnionStore computes the union entirely inside Redis (SUNIONSTORE): no
// member ever crosses the network.
func (s *Store) UnionStore(dest string, setKeys ...string) error {
	if len(setKeys) == 0 {
		return s.Delete(dest) // SUNIONSTORE with no sources is a Redis error
	}
	if err := s.rdb.SUnionStore(s.ctx(), s.k(dest), s.ks(setKeys)...).Err(); err != nil {
		return fmt.Errorf("redis: SUNIONSTORE %q: %w", dest, err)
	}
	return nil
}

func (s *Store) Delete(keys ...string) error {
	if len(keys) == 0 {
		return nil // DEL with no keys is a Redis error
	}
	if err := s.rdb.Del(s.ctx(), s.ks(keys)...).Err(); err != nil {
		return fmt.Errorf("redis: DEL: %w", err)
	}
	return nil
}

func (s *Store) PutDocument(entity, id string, raw []byte) error {
	if err := s.rdb.Set(s.ctx(), s.docKey(entity, id), raw, 0).Err(); err != nil {
		return fmt.Errorf("redis: SET %s:%s: %w", entity, id, err)
	}
	return nil
}

func (s *Store) GetDocument(entity, id string) ([]byte, error) {
	raw, err := s.rdb.Get(s.ctx(), s.docKey(entity, id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		// Translate the backend-specific "not found" into the port's error,
		// so callers never need to import go-redis to handle it.
		return nil, fmt.Errorf("document not found: %s:%s", entity, id)
	}
	if err != nil {
		return nil, fmt.Errorf("redis: GET %s:%s: %w", entity, id, err)
	}
	return raw, nil
}
