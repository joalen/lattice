package main

import (
	"context"
	"fmt"
	"math/rand"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/seed"
)

// Both comparisons below go straight to go-redis: they compare Redis commands,
// which the ports deliberately hide.

type microGroup struct {
	Title string
	Note  string
	Rows  []opResult
}

func microBenchmarks(ctx context.Context, rdb *goredis.Client, prefix string, store ports.SetStore, r runner) ([]microGroup, error) {
	key := func(path, value string) string { return prefix + keyspace.Facet(seed.Entity, path, value) }
	a, b := key("tier", "free"), key("group", "group-00")
	score := prefix + keyspace.Range(seed.Entity, "score")
	dst := prefix + seed.Entity + ":_tmp:bench"

	sizeA, err := rdb.SCard(ctx, a).Result()
	if err != nil {
		return nil, err
	}
	sizeB, err := rdb.SCard(ctx, b).Result()
	if err != nil {
		return nil, err
	}
	inter, err := rdb.SInterCard(ctx, 0, a, b).Result()
	if err != nil {
		return nil, err
	}

	intersect := microGroup{
		Title: "Intersection: SINTERSTORE vs in-app",
		Note: fmt.Sprintf("tier=free (%d ids) ∩ group=group-00 (%d ids) = %d ids. The column \"result\" is the intersection size.",
			sizeA, sizeB, inter),
	}
	run := func(g *microGroup, name string, fn func() (int, error)) {
		res := r.measure(name, func(*rand.Rand) (int, error) { return fn() })
		g.Rows = append(g.Rows, res)
	}
	run(&intersect, "SINTERSTORE into a temp key, count stays server-side", func() (int, error) {
		tx := rdb.TxPipeline()
		n := tx.SInterStore(ctx, dst, a, b)
		tx.Del(ctx, dst)
		_, err := tx.Exec(ctx)
		return int(n.Val()), err
	})
	run(&intersect, "SINTERCARD (count only)", func() (int, error) {
		n, err := rdb.SInterCard(ctx, 0, a, b).Result()
		return int(n), err
	})
	run(&intersect, "SINTER (server computes, ids come back)", func() (int, error) {
		ids, err := rdb.SInter(ctx, a, b).Result()
		return len(ids), err
	})
	run(&intersect, "in-app: SMEMBERS both, intersect in Go", func() (int, error) {
		pipe := rdb.Pipeline()
		ca, cb := pipe.SMembers(ctx, a), pipe.SMembers(ctx, b)
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, err
		}
		seen := make(map[string]struct{}, len(ca.Val()))
		for _, m := range ca.Val() {
			seen[m] = struct{}{}
		}
		n := 0
		for _, m := range cb.Val() {
			if _, ok := seen[m]; ok {
				n++
			}
		}
		return n, nil
	})

	groups := []microGroup{intersect}
	for _, off := range []int{0, int(sizeA / 2)} {
		g := microGroup{
			Title: fmt.Sprintf("Pagination, 20 results at offset %d of tier=free (%d ids), ordered by score", off, sizeA),
			Note:  "SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.",
		}
		run(&g, "SORT ... ALPHA LIMIT  (PRD approach 1)", func() (int, error) {
			ids, err := rdb.Sort(ctx, a, &goredis.Sort{Alpha: true, Offset: int64(off), Count: 20}).Result()
			return len(ids), err
		})
		run(&g, "ZINTERSTORE + ZRANGE  (PRD approach 2)", func() (int, error) {
			tx := rdb.TxPipeline()
			tx.ZInterStore(ctx, dst, &goredis.ZStore{Keys: []string{a, score}, Weights: []float64{0, 1}})
			page := tx.ZRevRange(ctx, dst, int64(off), int64(off+19))
			tx.Del(ctx, dst)
			_, err := tx.Exec(ctx)
			return len(page.Val()), err
		})
		run(&g, "current: fetch all ids, ZSCORE in batches, sort in Go", func() (int, error) {
			res, err := search.Find(ctx, store, search.Query{
				Entity: seed.Entity,
				Facets: []search.Facet{{Path: "tier", Values: []string{"free"}}},
				Sort:   &search.Sort{Path: "score", Desc: true},
				Offset: off,
				Limit:  20,
			})
			return len(res.IDs), err
		})
		groups = append(groups, g)
	}

	return groups, nil
}
