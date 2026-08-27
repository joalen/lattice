package ingest

import (
	"encoding/json"
	"fmt"
)

const SimilarityThreshold = 0.7

type cluster struct {
	id    string
	shape map[string]int
	count int
}

type ShapeIndex struct {
	object []cluster
	array  []cluster
}

func NewShapeIndex() *ShapeIndex {
	return &ShapeIndex{}
}

// Classify returns a stable cluster label for the node shape
func (idx *ShapeIndex) Classify(node any) string {
	switch v := node.(type) {
	case map[string]any:
		shape := make(map[string]int)

		for _, val := range v {
			shape[idx.Classify(val)]++
		}

		return idx.assign(&idx.object, "object", shape)

	case []any:
		shape := make(map[string]int)
		for _, item := range v {
			shape[idx.Classify(item)]++
		}
		return idx.assign(&idx.array, "array", shape)

	default:
		return kindOf(node)
	}
}

// assign finds existing cluster whose shape is similar enough to merge into
// NOTE: I would need to refactor this to be robust against combinatorial blowup
func (idx *ShapeIndex) assign(clusters *[]cluster, prefix string, shape map[string]int) string {
	for i := range *clusters {
		c := &(*clusters)[i]
		if ShapeSimilarity(c.shape, shape) >= SimilarityThreshold {
			c.count++
			return c.id
		}
	}
	id := fmt.Sprintf("%s#%d", prefix, len(*clusters))
	*clusters = append(*clusters, cluster{id: id, shape: shape, count: 1})
	return id
}

// Clusters reports every discovered recurring type and how often it occured
func (idx *ShapeIndex) Clusters() map[string]int {
	out := make(map[string]int)
	for _, c := range idx.object {
		out[c.id] = c.count
	}
	for _, c := range idx.array {
		out[c.id] = c.count
	}
	return out
}

// DiscoverClusters walks a whole corpus with ONE shared ShapeIndex, so that clustering happens across every document, not per-file.
func DiscoverClusters(raws [][]byte) (map[string]int, error) {
	idx := NewShapeIndex()
	for i, raw := range raws {
		var root any
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("discover clusters: document %d: %w", i, err)
		}
		idx.Classify(root) // classifies the ENTIRE subtree; we don't need the top-level label itself
	}
	return idx.Clusters(), nil
}
