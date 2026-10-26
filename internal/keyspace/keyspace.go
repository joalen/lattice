// Package keyspace is the single place index keys are spelled.
package keyspace

import "strings"

// Normalize lower-cases, trims and joins whitespace with underscores: "Computer Science" -> "computer_science".
func Normalize(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(v)), "_")
}

func Universe(entity string) string { return entity + ":universe" }

func FacetPrefix(entity, path string) string { return entity + ":" + path + ":" }

func Facet(entity, path, value string) string {
	return FacetPrefix(entity, path) + Normalize(value)
}

// Memberships holds the facet keys a document currently sits in, so re-ingestion can diff instead of flush.
func Memberships(entity, id string) string { return entity + ":_memberships:" + id }

// Values lists the normalized values seen for a field, so facet counts need not scan the keyspace.
// Entries are never removed: a value can outlive its last document, and callers drop zero counts.
func Values(entity, path string) string { return entity + ":_values:" + path }

func RangePrefix(entity string) string { return entity + ":_range:" }

// Range is the sorted set of document ID -> numeric value for one field.
func Range(entity, path string) string { return RangePrefix(entity) + path }

func IsRange(entity, key string) bool { return strings.HasPrefix(key, RangePrefix(entity)) }

// IsFacet reports whether key is a categorical facet set, as opposed to the
// universe, a range, a reverse index or a temp key.
func IsFacet(entity, key string) bool {
	if !strings.HasPrefix(key, entity+":") || key == Universe(entity) {
		return false
	}
	rest := strings.TrimPrefix(key, entity+":")
	return !strings.HasPrefix(rest, "_")
}
