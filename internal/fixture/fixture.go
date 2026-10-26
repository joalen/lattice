package fixture

import (
	"testing"

	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/ports"
)

const Users = `[
 {"id":1,"name":"Ada","role":"admin","active":true,"age":36,"address":{"city":"Gwenborough"},"tags":["go","redis"],"nickname":null,"orders":[{"sku":"a"}]},
 {"id":2,"name":"Bob","role":"student","active":true,"age":20,"address":{"city":"Wisokyburgh"},"tags":["go"],"orders":[]},
 {"id":3,"name":"Cy","role":"student","active":false,"age":22,"address":{"city":"Gwenborough"},"tags":["sql","redis"]},
 {"id":4,"name":"Di","role":"teacher","active":true,"age":45,"address":{"city":"Wisokyburgh"},"tags":["go","sql"]},
 {"id":5,"name":"Ed","role":"student","active":true,"age":19,"address":{"city":"Gwenborough"},"tags":["redis"]},
 {"id":6,"name":"Flo","role":"admin","active":false,"age":51,"address":{"city":"McKenziehaven"},"tags":[]}
]`

// Index ingests Users into store under the entity "user" and returns the mapping used.
func Index(t testing.TB, store ports.SetStore) ingest.Mapping {
	t.Helper()

	docs, err := ingest.SplitDocuments([]byte(Users))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ingest.ProposeMapping("user", docs, ingest.ProposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Ingest(docs, ingest.Options{}); err != nil {
		t.Fatal(err)
	}
	return m
}
