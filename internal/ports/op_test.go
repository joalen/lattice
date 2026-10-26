package ports_test

import (
	"math"
	"testing"

	"github.com/joalen/lattice/internal/ports"
)

func TestOp_Validate(t *testing.T) {
	good := []ports.Op{
		ports.Add("k", "a"),
		ports.Add("k"),
		ports.Remove("k", "a"),
		ports.DeleteKey("k"),
		ports.PutDoc("plan", "1", []byte("{}")),
		ports.DelDoc("plan", "1"),
		ports.SetScore("k", "m", 1.5),
		ports.DelScore("k", "m"),
	}
	for i, op := range good {
		if err := op.Validate(); err != nil {
			t.Errorf("good[%d]: unexpected error: %v", i, err)
		}
	}

	bad := []ports.Op{
		{},
		{Kind: 99},
		ports.Add("", "a"),
		ports.PutDoc("", "1", nil),
		ports.DelDoc("plan", ""),
		ports.SetScore("k", "", 1),
		ports.SetScore("k", "m", math.NaN()),
		ports.DelScore("", "m"),
	}
	for i, op := range bad {
		if err := op.Validate(); err == nil {
			t.Errorf("bad[%d]: expected an error", i)
		}
	}
}
