package redis_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/setstore/contract"
	redisstore "github.com/joalen/lattice/internal/setstore/redis"
)

var testSeq atomic.Uint64

// Skips when Redis is unreachable; set LATTICE_REQUIRE_REDIS=1 to fail instead.
func client(t *testing.T) *goredis.Client {
	t.Helper()

	addr := os.Getenv("LATTICE_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		if os.Getenv("LATTICE_REQUIRE_REDIS") != "" {
			t.Fatalf("redis required but unreachable at %s: %v", addr, err)
		}
		t.Skipf("redis not reachable at %s: %v", addr, err)
	}
	return rdb
}

// Deletes by SCAN on prefix rather than FLUSHDB, so a shared instance is left alone.
func cleanup(rdb *goredis.Client, prefix string) {
	ctx := context.Background()
	iter := rdb.Scan(ctx, 0, prefix+"*", 200).Iterator()
	for iter.Next(ctx) {
		rdb.Del(ctx, iter.Val())
	}
}

func TestStore_Contract(t *testing.T) {
	rdb := client(t)

	contract.Run(t, func(t *testing.T) ports.SetStore {
		prefix := fmt.Sprintf("lattice-test:%d:%d:", time.Now().UnixNano(), testSeq.Add(1))
		t.Cleanup(func() { cleanup(rdb, prefix) })
		return redisstore.New(rdb, redisstore.WithPrefix(prefix))
	})
}

func TestStore_HonorsCancelledContext(t *testing.T) {
	rdb := client(t)
	prefix := fmt.Sprintf("lattice-test:%d:%d:", time.Now().UnixNano(), testSeq.Add(1))
	t.Cleanup(func() { cleanup(rdb, prefix) })
	store := redisstore.New(rdb, redisstore.WithPrefix(prefix))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	calls := map[string]func() error{
		"AddToSet":     func() error { return store.AddToSet(ctx, "a", "1") },
		"Members":      func() error { _, err := store.Members(ctx, "a"); return err },
		"MembersMulti": func() error { _, err := store.MembersMulti(ctx, "a", "b"); return err },
		"Scores":       func() error { _, err := store.Scores(ctx, "z", "1"); return err },
		"Keys":         func() error { _, err := store.Keys(ctx, ""); return err },
		"Apply":        func() error { return store.Apply(ctx, ports.Add("a", "1")) },
		"GetDocument":  func() error { _, err := store.GetDocument(ctx, "e", "1"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s with a cancelled context returned %v, want context.Canceled", name, err)
		}
	}

	if n, err := store.Cardinality(t.Context(), "a"); err != nil || n != 0 {
		t.Errorf("a cancelled Apply/AddToSet must not write: cardinality %d, err %v", n, err)
	}
}
