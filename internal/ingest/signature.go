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
