package main

import (
	"context"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestPercentile(t *testing.T) {
	var s []time.Duration
	for i := 1; i <= 100; i++ {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	cases := map[float64]time.Duration{50: 50 * time.Millisecond, 95: 95 * time.Millisecond, 99: 99 * time.Millisecond, 100: 100 * time.Millisecond, 0: time.Millisecond}
	for p, want := range cases {
		if got := percentile(s, p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("empty = %v", got)
	}
	if got := percentile([]time.Duration{time.Second}, 99); got != time.Second {
		t.Errorf("single sample = %v", got)
	}
}

func TestMedianAndFormat(t *testing.T) {
	if got := median([]int64{9, 1, 5}); got != 5 {
		t.Errorf("median = %d", got)
	}
	if got := median[int](nil); got != 0 {
		t.Errorf("empty median = %d", got)
	}
	for d, want := range map[time.Duration]string{
		800 * time.Nanosecond:   "800 ns",
		450 * time.Microsecond:  "450 µs",
		2500 * time.Microsecond: "2.50 ms",
		3 * time.Second:         "3.00 s",
	} {
		if got := fmtDur(d); got != want {
			t.Errorf("fmtDur(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPickMany(t *testing.T) {
	rng := newRand(1)
	pool := []string{"a", "b", "c", "d", "e"}
	for i := 0; i < 50; i++ {
		got := pickMany(rng, pool, "c", 3)
		seen := map[string]bool{}
		for _, v := range got {
			seen[v] = true
		}
		if len(got) != 3 || len(seen) != 3 || got[0] != "c" {
			t.Fatalf("got %v", got)
		}
	}
}

// Needs a Redis; counts what a pipeline, a transaction and a plain command each cost.
func TestTripCounter(t *testing.T) {
	addr := os.Getenv("LATTICE_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	defer rdb.Close()
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not reachable: %v", err)
	}

	c := &tripCounter{}
	rdb.AddHook(c)
	before := c.now()
	rdb.Get(ctx, "lattice-bench-trip-test")
	p := rdb.Pipeline()
	p.Get(ctx, "lattice-bench-trip-test")
	p.Get(ctx, "lattice-bench-trip-test")
	_, _ = p.Exec(ctx)
	tx := rdb.TxPipeline()
	tx.Get(ctx, "lattice-bench-trip-test")
	tx.Get(ctx, "lattice-bench-trip-test")
	_, _ = tx.Exec(ctx)

	if got := c.now() - before; got != 3 {
		t.Errorf("one command + one pipeline + one transaction counted %d round trips, want 3", got)
	}
}
