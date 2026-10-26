package ports

type SetStore interface {
	AddToSet(setKey string, members ...string) error
	Intersect(setKeys ...string) ([]string, error)
	Union(setKeys ...string) ([]string, error)
	Difference(setKeys ...string) ([]string, error)
	Cardinality(setKey string) (int, error)

	UnionStore(dest string, setKeys ...string) error
	Delete(keys ...string) error

	PutDocument(entity, id string, raw []byte) error
	GetDocument(entity, id string) ([]byte, error)
}
