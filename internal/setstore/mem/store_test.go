package mem_test

import (
	"testing"

	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/setstore/contract"
	"github.com/joalen/lattice/internal/setstore/mem"
)

func TestStore_Contract(t *testing.T) {
	contract.Run(t, func(t *testing.T) ports.SetStore {
		return mem.New()
	})
}
