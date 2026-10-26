package ports_test

import (
	"testing"

	"github.com/joalen/lattice/internal/ports"
)

func TestOp_Validate(t *testing.T) {
	good := []ports.Op{
		ports.Add("k", "a"),
		ports.Add("k"), // empty member list is legal (a no-op)
		ports.Remove("k", "a"),
		ports.DeleteKey("k"),
		ports.PutDoc("plan", "1", []byte("{}")),
		ports.DelDoc("plan", "1"),
	}
	for i, op := range good {
		if err := op.Validate(); err != nil {
			t.Errorf("good[%d]: unexpected error: %v", i, err)
		}
	}

	bad := []ports.Op{
		{},                         // zero value: no kind
		{Kind: 99},                 // unknown kind
		ports.Add("", "a"),         // missing key
		ports.PutDoc("", "1", nil), // missing entity
		ports.DelDoc("plan", ""),   // missing id
	}
	for i, op := range bad {
		if err := op.Validate(); err == nil {
			t.Errorf("bad[%d]: expected an error", i)
		}
	}
}
