package ports

import "errors"

var ErrNotFound = errors.New("not found")

type SetStore interface {
	AddToSet(setKey string, members ...string) error
	Members(setKey string) ([]string, error)
	Intersect(setKeys ...string) ([]string, error)
	Union(setKeys ...string) ([]string, error)
	Difference(setKeys ...string) ([]string, error)
	Cardinality(setKey string) (int, error)

	// Store variants keep the work server-side (SUNIONSTORE / SINTERSTORE in Redis)
	UnionStore(dest string, setKeys ...string) error
	IntersectStore(dest string, setKeys ...string) error
	IntersectCard(setKeys ...string) (int, error)
	Delete(keys ...string) error

	// Keys lists set and sorted-set keys that start with prefix.
	Keys(prefix string) ([]string, error)

	// RangeByScore returns members with min <= score <= max, ascending by score then member.
	RangeByScore(key string, min, max float64) ([]string, error)
	// Scores omits members that have no score under key.
	Scores(key string, members ...string) (map[string]float64, error)

	// Apply runs ops in order, all or nothing: any invalid op rejects the whole batch.
	Apply(ops ...Op) error

	PutDocument(entity, id string, raw []byte) error
	GetDocument(entity, id string) ([]byte, error)
}
