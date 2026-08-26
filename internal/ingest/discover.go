package ingest

import "encoding/json"

// Discover walks an arbitrary JSON document and "tallies" how often each
// structural Signature occurs anywhere in the tree. Fully domain agnostic,
// where we make no assumptions about field names, top-level shape, or
// what the data represents.
func Discover(raw []byte) (map[string]int, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}

	freq := make(map[string]int)
	tally(root, freq)
	return freq, nil
}

// a counter for all the fields encountered within a node and stores it as a frequency mapping
func tally(node any, freq map[string]int) {
	freq[Signature(node)]++ // count this node's own shape

	switch v := node.(type) {
	case map[string]any:
		for _, child := range v {
			tally(child, freq) // then recurse into every field
		}
	case []any:
		for _, item := range v {
			tally(item, freq) // and every array element
		}
	}
}
