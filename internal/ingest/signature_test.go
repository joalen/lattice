package ingest

import "testing"

func TestShapeSimilarity_ThresholdBehavior(t *testing.T) {
	tests := []struct {
		name      string
		a, b      map[string]int
		wantMerge bool // does 0.7 correctly decide whether these should merge?
	}{
		{
			name:      "large object (10 fields), missing 1 optional field",
			a:         map[string]int{"string": 10},
			b:         map[string]int{"string": 9},
			wantMerge: true,
		},
		{
			name:      "tiny object (2 fields), missing 1 optional field",
			a:         map[string]int{"string": 2},
			b:         map[string]int{"string": 1},
			wantMerge: false,
		},
		{
			name:      "identical shapes",
			a:         map[string]int{"string": 6, "object": 1},
			b:         map[string]int{"string": 6, "object": 1},
			wantMerge: true,
		},
		{
			name:      "completely unrelated shapes",
			a:         map[string]int{"string": 6, "object": 1},
			b:         map[string]int{"number": 3, "array{string}": 2},
			wantMerge: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := ShapeSimilarity(tt.a, tt.b)
			merged := sim >= SimilarityThreshold

			t.Logf("similarity = %.3f (threshold %.2f)", sim, SimilarityThreshold)

			if merged != tt.wantMerge {
				t.Errorf("got merge=%v, want merge=%v (similarity was %.3f)", merged, tt.wantMerge, sim)
			}
		})
	}
}

// TestShapeSimilarity_SemanticCollision proves a real limitation directly:
// two objects representing totally unrelated real-world entities can score
// a PERFECT 1.0 similarity if their primitive-kind composition matches.
func TestShapeSimilarity_SemanticCollision(t *testing.T) {
	personRecord := map[string]int{"string": 6, "number": 1}  // name, email, phone, city, state, zip + age
	productRecord := map[string]int{"string": 6, "number": 1} // title, sku, color, size, material, description + price

	sim := ShapeSimilarity(personRecord, productRecord)
	if sim != 1.0 {
		t.Fatalf("expected a coincidental perfect match, got %.3f", sim)
	}
	t.Logf("person record vs. product record: %.3f similarity despite being unrelated entities", sim)
}
