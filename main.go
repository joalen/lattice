package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/joalen/lattice/internal/ingest"
)

type sigCount struct {
	Signature string
	Count     int
}

func main() {
	raw, err := os.ReadFile("")
	if err != nil {
		panic(err)
	}

	freq, err := ingest.Discover(raw)
	if err != nil {
		panic(err)
	}

	counts := make([]sigCount, 0, len(freq))
	for sig, n := range freq {
		counts = append(counts, sigCount{sig, n})
	}

	sort.Slice(counts, func(i, j int) bool {
		return counts[i].Count > counts[j].Count // most frequent shape first
	})

	for _, c := range counts {
		fmt.Printf("[%dx] %s\n", c.Count, c.Signature)
	}
}
