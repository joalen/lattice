package ingest

import "testing"

func TestShouldMerge_ThresholdBehavior(t *testing.T) {
	tests := []struct {
		name      string
		a, b      map[string]int
		wantMerge bool
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
			wantMerge: true,
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
		{
			name:      "two different single-field kinds should NOT merge via the absolute-diff escape hatch",
			a:         map[string]int{"string": 1},
			b:         map[string]int{"number": 1},
			wantMerge: false, // symmetric difference is 2 here (1 lost + 1 gained), not 1 -- confirms the hatch isn't too permissive
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := ShapeSimilarity(tt.a, tt.b)
			diff := symmetricDifference(tt.a, tt.b)
			got := ShouldMerge(tt.a, tt.b)

			t.Logf("similarity=%.3f  symmetricDiff=%d  merge=%v", sim, diff, got)

			if got != tt.wantMerge {
				t.Errorf("got merge=%v, want merge=%v", got, tt.wantMerge)
			}
		})
	}
}

func TestShapeSimilarity_SemanticCollision(t *testing.T) {
	personRecord := map[string]int{"string": 6, "number": 1}
	productRecord := map[string]int{"string": 6, "number": 1}

	sim := ShapeSimilarity(personRecord, productRecord)
	if sim != 1.0 {
		t.Fatalf("expected a coincidental perfect match, got %.3f", sim)
	}
	t.Logf("person record vs. product record: %.3f similarity despite being unrelated entities", sim)
}
