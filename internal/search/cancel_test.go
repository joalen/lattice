package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/joalen/lattice/internal/fixture"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/setstore/mem"
)

// cancelOnRange behaves like a network adapter once ctx is cancelled: every call fails.
type cancelOnRange struct {
	ports.SetStore
	cancel func()
}

func (c *cancelOnRange) RangeByScore(ctx context.Context, key string, min, max float64) ([]string, error) {
	c.cancel()
	return nil, ctx.Err()
}

func (c *cancelOnRange) Delete(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.SetStore.Delete(ctx, keys...)
}

func TestMatch_CancelledQueryLeavesNoTempKeys(t *testing.T) {
	base := mem.New()
	fixture.Index(t, base)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &cancelOnRange{SetStore: base, cancel: cancel}

	// The OR list creates a temp key before the range lookup cancels the request.
	_, err := search.Match(ctx, store, search.Query{
		Entity: "user",
		Facets: []search.Facet{{Path: "role", Values: []string{"student", "admin"}}},
		Ranges: []search.Range{{Path: "age", Min: f64(20)}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}

	left, err := base.Keys(t.Context(), "user:_tmp:")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("temp keys leaked after cancellation: %v", left)
	}
}
