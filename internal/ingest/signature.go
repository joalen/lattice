package ingest

import (
	"sort"
	"strings"
)

// Signature computes a structural fingerprint of a JSON node to where we capture what
// the JSON LOOKS like without requiring literal key names (this comes if you have JSONs
// that may not all look uniform but have an underlying, similar, and implied schema). Therefore,
// you can have two nodes with different keys BUT same shape produce the EXACT signature!
func Signature(node any) string {
	switch v := node.(type) {

	case map[string]any:
		kinds := make([]string, 0, len(v))

		for key, val := range v {
			kinds = append(kinds, kindOf(val))
			_ = key
		}

		sort.Strings(kinds)
		return "object{" + strings.Join(kinds, ",") + "}"

	case []any:
		if len(v) == 0 {
			return "array{}"
		}

		return "array{" + Signature(v[0]) + "}"

	default:
		return kindOf(v)
	}
}

// KindCounts returns a multiset (aka Counter) of immediate children kinds for a node
// e.g. an object with 6 string fields and 1 nested object returns
// {"string": 6, "object{...}": 1}. Unlike Signature, this doesn't collapse
// into one string, so two shapes can be compared for approximate match
// rather than only exact match.
func KindCounts(node any) map[string]int {
	counts := make(map[string]int)

	switch v := node.(type) {
	case map[string]any:
		for _, val := range v {
			counts[kindOf(val)]++
		}

	case []any:
		for _, item := range v {
			counts[kindOf(item)]++
		}
	}

	return counts
}

func ShapeSimilarity(a, b map[string]int) float64 {
	allKinds := make(map[string]struct{})

	for k := range a {
		allKinds[k] = struct{}{}
	}

	for k := range b {
		allKinds[k] = struct{}{}
	}

	if len(allKinds) == 0 {
		return 1.0
	}

	var intersection, union int
	for kind := range allKinds {
		ca, cb := a[kind], b[kind] // missing key in a map will return a zero value (0)

		if ca < cb {
			intersection += ca
		} else {
			intersection += cb
		}

		if ca > cb {
			union += ca
		} else {
			union += cb
		}
	}
	return float64(intersection) / float64(union)
}

// kindOf returns a coarse type label and helps derive children types if objects/array are in the payload
func kindOf(v any) string {
	switch v.(type) {

	case map[string]any, []any:
		return Signature(v)

	case string:
		return "string"

	case float64:
		return "number"

	case bool:
		return "bool"

	case nil:
		return "null"

	default:
		return "unknown"
	}
}
