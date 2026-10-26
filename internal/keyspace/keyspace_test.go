package keyspace_test

import (
	"testing"

	"github.com/joalen/lattice/internal/keyspace"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Computer Science":   "computer_science",
		"  BS ":              "bs",
		"a   b\tc":           "a_b_c",
		"already_normalized": "already_normalized",
		"":                   "",
	}
	for in, want := range cases {
		if got := keyspace.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeys(t *testing.T) {
	if got := keyspace.Facet("plan", "major", "Computer Science"); got != "plan:major:computer_science" {
		t.Errorf("Facet = %q", got)
	}
	if got := keyspace.FacetPrefix("plan", "address.city") + keyspace.Normalize("Gwen Borough"); got != keyspace.Facet("plan", "address.city", "Gwen Borough") {
		t.Errorf("FacetPrefix + Normalize should equal Facet, got %q", got)
	}
	if keyspace.Universe("plan") != "plan:universe" {
		t.Error("Universe")
	}
	if keyspace.Memberships("plan", "7") != "plan:_memberships:7" {
		t.Error("Memberships")
	}

	if keyspace.Range("plan", "credits") != "plan:_range:credits" {
		t.Error("Range")
	}
	if !keyspace.IsRange("plan", "plan:_range:credits") || keyspace.IsRange("plan", "plan:major:cs") {
		t.Error("IsRange")
	}
}

func TestIsFacet(t *testing.T) {
	yes := []string{"plan:major:cs", "plan:address.city:x"}
	no := []string{"plan:universe", "plan:_range:credits", "plan:_memberships:1", "plan:_tmp:abc", "other:major:cs"}
	for _, k := range yes {
		if !keyspace.IsFacet("plan", k) {
			t.Errorf("IsFacet(%q) = false", k)
		}
	}
	for _, k := range no {
		if keyspace.IsFacet("plan", k) {
			t.Errorf("IsFacet(%q) = true", k)
		}
	}
}
