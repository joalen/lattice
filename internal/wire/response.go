// Package wire holds the JSON shapes lattice exchanges with callers: the CLI
// prints them and the FFI layer hands them to Java. It imports nothing from the
// rest of the module, so renaming an internal type cannot change what callers see.
package wire

type DiscoverEntry struct {
	Signature string `json:"signature"`
	Count     int    `json:"count"`
}

type IngestReport struct {
	Docs        int `json:"docs"`
	Created     int `json:"created"`
	Updated     int `json:"updated"`
	Unchanged   int `json:"unchanged"`
	Pruned      int `json:"pruned"`
	KeysAdded   int `json:"keys_added"`
	KeysRemoved int `json:"keys_removed"`
}

type FacetCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type SearchHit struct {
	ID      string         `json:"id"`
	Summary map[string]any `json:"summary"`
}

type SearchResponse struct {
	Total   int                     `json:"total"`
	Offset  int                     `json:"offset"`
	Results []SearchHit             `json:"results"`
	Facets  map[string][]FacetCount `json:"facets,omitempty"`
}

type SimilarMatch struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Shared  int            `json:"shared"`
	Summary map[string]any `json:"summary"`
}

type SimilarResponse struct {
	ID      string         `json:"id"`
	Matches []SimilarMatch `json:"matches"`
}
