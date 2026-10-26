// Package redis implements ports.SetStore on Redis or Valkey.
package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/ports"
)

var _ ports.SetStore = (*Store)(nil)

type Store struct {
	rdb    goredis.Cmdable
	prefix string
}

type Option func(*Store)

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

func (s *Store) k(key string) string { return s.prefix + key }

func (s *Store) ks(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = s.k(key)
	}
	return out
}

// docNS keeps documents from colliding with set keys (WRONGTYPE) in the shared keyspace.
const (
	docNS         = "_doc:"
	pipelineChunk = 1000
)

func (s *Store) docKey(entity, id string) string {
	return s.prefix + docNS + entity + ":" + id
}

func (s *Store) AddToSet(ctx context.Context, setKey string, members ...string) error {
	if len(members) == 0 {
		return nil // SADD errors on zero members
	}
	if err := s.rdb.SAdd(ctx, s.k(setKey), toAny(members)...).Err(); err != nil {
		return fmt.Errorf("redis: SADD %q: %w", setKey, err)
	}
	return nil
}

func (s *Store) Members(ctx context.Context, setKey string) ([]string, error) {
	out, err := s.rdb.SMembers(ctx, s.k(setKey)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SMEMBERS %q: %w", setKey, err)
	}
	return out, nil
}

func (s *Store) Intersect(ctx context.Context, setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return nil, errors.New("intersect: no set keys given")
	}
	out, err := s.rdb.SInter(ctx, s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SINTER: %w", err)
	}
	return out, nil
}

func (s *Store) Union(ctx context.Context, setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return []string{}, nil
	}
	out, err := s.rdb.SUnion(ctx, s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SUNION: %w", err)
	}
	return out, nil
}

func (s *Store) Difference(ctx context.Context, setKeys ...string) ([]string, error) {
	if len(setKeys) == 0 {
		return nil, errors.New("difference: no set keys given")
	}
	out, err := s.rdb.SDiff(ctx, s.ks(setKeys)...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: SDIFF: %w", err)
	}
	return out, nil
}

func (s *Store) Cardinality(ctx context.Context, setKey string) (int, error) {
	n, err := s.rdb.SCard(ctx, s.k(setKey)).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: SCARD %q: %w", setKey, err)
	}
	return int(n), nil
}

func (s *Store) UnionStore(ctx context.Context, dest string, setKeys ...string) error {
	if len(setKeys) == 0 {
		return s.Delete(ctx, dest) // SUNIONSTORE errors on zero sources
	}
	if err := s.rdb.SUnionStore(ctx, s.k(dest), s.ks(setKeys)...).Err(); err != nil {
		return fmt.Errorf("redis: SUNIONSTORE %q: %w", dest, err)
	}
	return nil
}

func (s *Store) IntersectStore(ctx context.Context, dest string, setKeys ...string) error {
	if len(setKeys) == 0 {
		return errors.New("intersect: no set keys given")
	}
	if err := s.rdb.SInterStore(ctx, s.k(dest), s.ks(setKeys)...).Err(); err != nil {
		return fmt.Errorf("redis: SINTERSTORE %q: %w", dest, err)
	}
	return nil
}

// SINTERCARD needs Redis 7.0 or Valkey 7.2.
func (s *Store) IntersectCard(ctx context.Context, setKeys ...string) (int, error) {
	if len(setKeys) == 0 {
		return 0, errors.New("intersect: no set keys given")
	}
	n, err := s.rdb.SInterCard(ctx, 0, s.ks(setKeys)...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: SINTERCARD: %w", err)
	}
	return int(n), nil
}

func (s *Store) IntersectCards(ctx context.Context, base string, setKeys ...string) ([]int, error) {
	out := make([]int, len(setKeys))

	for start := 0; start < len(setKeys); start += pipelineChunk {
		batch := setKeys[start:min(start+pipelineChunk, len(setKeys))]
		pipe := s.rdb.Pipeline()
		cmds := make([]*goredis.IntCmd, len(batch))
		for i, k := range batch {
			cmds[i] = pipe.SInterCard(ctx, 0, s.k(base), s.k(k))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("redis: SINTERCARD batch: %w", err)
		}
		for i, c := range cmds {
			out[start+i] = int(c.Val())
		}
	}
	return out, nil
}

func (s *Store) Keys(ctx context.Context, prefix string) ([]string, error) {
	pattern := globEscape(s.prefix+prefix) + "*"

	seen := make(map[string]struct{})
	var cursor uint64
	for {
		batch, next, err := s.rdb.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return nil, fmt.Errorf("redis: SCAN %q: %w", prefix, err)
		}
		for _, k := range batch {
			k = strings.TrimPrefix(k, s.prefix)
			if !strings.HasPrefix(k, docNS) {
				seen[k] = struct{}{}
			}
		}
		if cursor = next; cursor == 0 {
			break
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) RangeByScore(ctx context.Context, key string, min, max float64) ([]string, error) {
	out, err := s.rdb.ZRangeByScore(ctx, s.k(key), &goredis.ZRangeBy{Min: bound(min), Max: bound(max)}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: ZRANGEBYSCORE %q: %w", key, err)
	}
	return out, nil
}

func (s *Store) Scores(ctx context.Context, key string, members ...string) (map[string]float64, error) {
	out := make(map[string]float64, len(members))

	for start := 0; start < len(members); start += pipelineChunk {
		batch := members[start:min(start+pipelineChunk, len(members))]
		pipe := s.rdb.Pipeline()
		cmds := make([]*goredis.FloatCmd, len(batch))
		for i, m := range batch {
			cmds[i] = pipe.ZScore(ctx, s.k(key), m)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
			return nil, fmt.Errorf("redis: ZSCORE %q: %w", key, err)
		}
		for i, c := range cmds {
			score, err := c.Result()
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("redis: ZSCORE %q: %w", key, err)
			}
			out[batch[i]] = score
		}
	}
	return out, nil
}

func (s *Store) MembersMulti(ctx context.Context, setKeys ...string) (map[string][]string, error) {
	out := make(map[string][]string, len(setKeys))

	for start := 0; start < len(setKeys); start += pipelineChunk {
		batch := setKeys[start:min(start+pipelineChunk, len(setKeys))]
		pipe := s.rdb.Pipeline()
		cmds := make([]*goredis.StringSliceCmd, len(batch))
		for i, k := range batch {
			cmds[i] = pipe.SMembers(ctx, s.k(k))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("redis: SMEMBERS batch: %w", err)
		}
		for i, c := range cmds {
			out[batch[i]] = c.Val()
		}
	}
	return out, nil
}

func bound(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`).Replace(s)
}

func (s *Store) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil // DEL errors on zero keys
	}
	if err := s.rdb.Del(ctx, s.ks(keys)...).Err(); err != nil {
		return fmt.Errorf("redis: DEL: %w", err)
	}
	return nil
}

// MULTI/EXEC isolates the batch but does not roll back if a command fails at runtime.
func (s *Store) Apply(ctx context.Context, ops ...ports.Op) error {
	for _, op := range ops {
		if err := op.Validate(); err != nil {
			return err
		}
	}

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
		case ports.OpSetScore:
			pipe.ZAdd(ctx, s.k(op.Key), goredis.Z{Score: op.Score, Member: op.Member})
		case ports.OpDelScore:
			pipe.ZRem(ctx, s.k(op.Key), op.Member)
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

func (s *Store) PutDocument(ctx context.Context, entity, id string, raw []byte) error {
	if err := s.rdb.Set(ctx, s.docKey(entity, id), raw, 0).Err(); err != nil {
		return fmt.Errorf("redis: SET %s:%s: %w", entity, id, err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, entity, id string) ([]byte, error) {
	raw, err := s.rdb.Get(ctx, s.docKey(entity, id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("%w: document %s:%s", ports.ErrNotFound, entity, id)
	}
	if err != nil {
		return nil, fmt.Errorf("redis: GET %s:%s: %w", entity, id, err)
	}
	return raw, nil
}
