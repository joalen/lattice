package ports

import "errors"

// ErrNotFound is returned (wrapped) when a requested document does not exist.
// Callers test for it with errors.Is(err, ports.ErrNotFound), which lets them
// tell "missing" apart from "the backend is down" without importing any
// adapter package.
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
	Delete(keys ...string) error

	// Apply runs the ops in order as one atomic unit: every op is validated
	// first, and if any is invalid nothing is applied.
	Apply(ops ...Op) error

	PutDocument(entity, id string, raw []byte) error
	GetDocument(entity, id string) ([]byte, error)
}
