// rule to maintain: "no raw string key-building" outside a single helper
// package. Therefore, ingest writes keys with these functions and search reads them with
// the same ones, so the two can never drift apart (for example, in how values
// are normalized).
package keyspace

import "strings"

// Normalize makes a field value safe and comparable as part of a key:
// lower-cased, trimmed, inner whitespace collapsed to underscores
// ("Computer Science" -> "computer_science"). Both indexing and querying must
// pass values through it.
func Normalize(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(v)), "_")
}

// Universe is the set of every document ID for an entity.
func Universe(entity string) string { return entity + ":universe" }

// FacetPrefix is the shared front of every set key for one field
func FacetPrefix(entity, path string) string { return entity + ":" + path + ":" }

// Facet is the set of IDs whose field at path has the given value.
func Facet(entity, path, value string) string {
	return FacetPrefix(entity, path) + Normalize(value)
}

// Memberships is a reverse index: the set of facet keys a document currently
// belongs to. It is what makes re-ingestion a cheap diff instead of a flush,
// because we can see exactly which sets an old version of a document sat in.
func Memberships(entity, id string) string { return entity + ":_memberships:" + id }
