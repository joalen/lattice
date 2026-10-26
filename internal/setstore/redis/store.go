// Package redis is the Redis (and Valkey) adapter for ports.SetStore.
//
// Each port method maps to one Redis command, which is why this adapter is
// thin: the whole point of the project is that set theory already *is* the
// Redis command set.
//
//	AddToSet     -> SADD
//	Intersect    -> SINTER
//	Union        -> SUNION
//	Difference   -> SDIFF
//	Cardinality  -> SCARD
//	Members      -> SMEMBERS
//	UnionStore   -> SUNIONSTORE
//	Apply        -> MULTI ... EXEC
//	Delete       -> DEL
//	PutDocument  -> SET
//	GetDocument  -> GET
package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/ports"
)

// Compile-time proof that *Store satisfies the port. If the interface grows
// and this adapter falls behind, the build breaks here, with a clear message.
var _ ports.SetStore = (*Store)(nil)

// Store implements ports.SetStore on top of Redis.
type Store struct {
	rdb    goredis.Cmdable
	prefix string
}

// Option configures a Store (the "functional options" pattern: optional
// settings without a constructor that grows a parameter per feature).
type Option func(*Store)

// WithPrefix namespaces every key this Store touches. Production can leave it
// empty; tests use a unique prefix per test so they can share one Redis
// without trampling each other or real data.
func WithPrefix(p string) Option {
	return func(s *Store) { s.prefix = p }
}

// New wraps a go-redis client. It accepts the goredis.Cmdable interface rather
// than *goredis.Client, so a Cluster or Ring client (or a test double) can be
// passed in later without changing this package.
func New(rdb goredis.Cmdable, opts ...Option) *Store {
	s := &Store{rdb: rdb}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// The port does not take a context yet, so the adapter supplies one. When
// timeouts and cancellation matter (HTTP/MCP layer), the right move is to add
// ctx as the first parameter on every port method; that is a mechanical change
// the contract suite will verify.
func (s *Store) ctx() context.Context { return context.Background() }

// ---- key helpers ----------------------------------------------------------
// All key-building lives here. Nothing outside this package should ever
// concatenate a prefix, per the PRD's namespacing rule.

func (s *Store) k(key string) string { return s.prefix + key }

func (s *Store) ks(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = s.k(key)
	}
	return out
}

// Documents and sets share one Redis keyspace, unlike mem.Store, which keeps
// them in separate maps. A "doc:" segment guarantees a document can never
// collide with a set key such as plan:universe (which would otherwise raise
// WRONGTYPE at runtime).
func (s *Store) docKey(entity, id string) string {
	return s.prefix + "doc:" + entity + ":" + id
}

// ---- sets -----------------------------------------------------------------

func (s *Store) AddToSet(setKey string, members ...string) error {
	if len(members) == 0 {
		return nil // SADD with no members is a Redis error; the contract says no-op
	}
	if err := s.rdb.SAdd(s.ctx(), s.k(setKey), toAny(members)...).Err(); err != nil {
		return fmt.Errorf("redis: SADD %q: %w", setKey, err)
	}
	return nil
}

// Members returns every element of a set (SMEMBERS). A missing key is the empty set.
func (s *Store) Members(setKey string) ([]string, error) {
	out, err := s.rdb.SMembers(s.ctx(), s.k(setKey)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SMEMBERS %q: %w", setKey, err)
	}
	return out, nil
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
// member ever crosses the network. This is what replaced the
// Union -> AddToSet round trip in search.Find. SUNIONSTORE overwrites dest, and
// deletes it if the result is empty.
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

// ---- atomic batches -------------------------------------------------------

// Apply queues every op in a MULTI/EXEC transaction: Redis runs the whole
// batch without interleaving any other client's commands. Ops are validated
// first, so a malformed op rejects the batch before anything is queued.
//
// Caveat worth knowing: MULTI/EXEC is atomic in *isolation*, not a rollback.
// If a command fails at runtime (say WRONGTYPE) the others still run. That is
// acceptable here because validated set/doc ops can only fail on corrupt keys.
func (s *Store) Apply(ops ...ports.Op) error {
	for _, op := range ops {
		if err := op.Validate(); err != nil {
			return err
		}
	}

	ctx := s.ctx()
	pipe := s.rdb.TxPipeline()
	queued := 0
	for _, op := range ops {
		switch op.Kind {
		case ports.OpAdd:
			if len(op.Members) == 0 {
				continue
			}
			pipe.SAdd(ctx, s.k(op.Key), toAny(op.Members)...)
		case ports.OpRemove:
			if len(op.Members) == 0 {
				continue
			}
			pipe.SRem(ctx, s.k(op.Key), toAny(op.Members)...)
		case ports.OpDelete:
			pipe.Del(ctx, s.k(op.Key))
		case ports.OpPutDoc:
			pipe.Set(ctx, s.docKey(op.Entity, op.ID), op.Raw, 0)
		case ports.OpDelDoc:
			pipe.Del(ctx, s.docKey(op.Entity, op.ID))
		}
		queued++
	}
	if queued == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: apply batch: %w", err)
	}
	return nil
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// ---- documents ------------------------------------------------------------

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
		return nil, fmt.Errorf("%w: document %s:%s", ports.ErrNotFound, entity, id)
	}
	if err != nil {
		return nil, fmt.Errorf("redis: GET %s:%s: %w", entity, id, err)
	}
	return raw, nil
}
