package wire

// FacetFilter matches documents whose field has any one of Values.
type FacetFilter struct {
	Path   string   `json:"path"`
	Values []string `json:"values"`
}

// RangeFilter is inclusive on both ends; a nil bound is open.
type RangeFilter struct {
	Path string   `json:"path"`
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
}

type SortSpec struct {
	Path string `json:"path"`
	Desc bool   `json:"desc,omitempty"`
}

// SearchRequest ANDs Facets and Ranges together, then subtracts anything in Not.
// A Limit of zero returns every match.
type SearchRequest struct {
	Entity      string        `json:"entity"`
	Facets      []FacetFilter `json:"facets,omitempty"`
	Not         []FacetFilter `json:"not,omitempty"`
	Ranges      []RangeFilter `json:"ranges,omitempty"`
	Sort        *SortSpec     `json:"sort,omitempty"`
	Limit       int           `json:"limit"`
	Offset      int           `json:"offset"`
	CountFacets []string      `json:"count_facets,omitempty"`
	Fields      []string      `json:"fields,omitempty"`
}

type GetRequest struct {
	Entity string `json:"entity"`
	ID     string `json:"id"`
}

// SimilarRequest compares every facet unless Paths narrows it. A Limit of zero returns every match.
type SimilarRequest struct {
	Entity string   `json:"entity"`
	ID     string   `json:"id"`
	Paths  []string `json:"paths,omitempty"`
	Limit  int      `json:"limit"`
	Fields []string `json:"fields,omitempty"`
}
