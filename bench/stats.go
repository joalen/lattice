package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// percentile uses the nearest-rank method on an ascending slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(1, min(rank, len(sorted)))
	return sorted[rank-1]
}

func median[T int | int64](vals []T) T {
	if len(vals) == 0 {
		return 0
	}
	s := append([]T(nil), vals...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2f s", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2f ms", float64(d)/1e6)
	case d < time.Microsecond:
		return fmt.Sprintf("%d ns", d.Nanoseconds())
	}
	return fmt.Sprintf("%.0f µs", float64(d)/1e3)
}

// tripCounter counts network round trips: one per command, one per pipeline or transaction.
type tripCounter struct{ n atomic.Int64 }

func (c *tripCounter) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (c *tripCounter) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmd)
	}
}

func (c *tripCounter) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmds)
	}
}

func (c *tripCounter) now() int64 {
	if c == nil {
		return 0
	}
	return c.n.Load()
}
