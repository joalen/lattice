package ports

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("not found")

type SetStore interface {
	AddToSet(ctx context.Context, setKey string, members ...string) error
	Members(ctx context.Context, setKey string) ([]string, error)
	// MembersMulti fetches several sets in one round trip; missing keys map to an empty slice.
	MembersMulti(ctx context.Context, setKeys ...string) (map[string][]string, error)
	Intersect(ctx context.Context, setKeys ...string) ([]string, error)
	Union(ctx context.Context, setKeys ...string) ([]string, error)
	Difference(ctx context.Context, setKeys ...string) ([]string, error)
	Cardinality(ctx context.Context, setKey string) (int, error)

	// Store variants keep the work server-side (SUNIONSTORE / SINTERSTORE in Redis)
	UnionStore(ctx context.Context, dest string, setKeys ...string) error
	IntersectStore(ctx context.Context, dest string, setKeys ...string) error
	IntersectCard(ctx context.Context, setKeys ...string) (int, error)
	// IntersectCards returns |base ∩ key| for each key in order, batching round trips.
	IntersectCards(ctx context.Context, base string, keys ...string) ([]int, error)
	Delete(ctx context.Context, keys ...string) error

	// Keys lists set and sorted-set keys that start with prefix.
	Keys(ctx context.Context, prefix string) ([]string, error)

	// RangeByScore returns members with min <= score <= max, ascending by score then member.
	RangeByScore(ctx context.Context, key string, min, max float64) ([]string, error)
	// Scores omits members that have no score under key.
	Scores(ctx context.Context, key string, members ...string) (map[string]float64, error)

	// Apply runs ops in order, all or nothing: any invalid op rejects the whole batch.
	Apply(ctx context.Context, ops ...Op) error

	PutDocument(ctx context.Context, entity, id string, raw []byte) error
	GetDocument(ctx context.Context, entity, id string) ([]byte, error)
}
