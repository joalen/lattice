package redis_test

import (
	"context"
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

// client connects to LATTICE_REDIS_ADDR (default localhost:6379). If nothing
// is listening, then test skipped rather than failed!
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
		// Unique per subtest, so subtests are isolated from each other and
		// from anything else living in this Redis.
		prefix := fmt.Sprintf("lattice-test:%d:%d:", time.Now().UnixNano(), testSeq.Add(1))
		t.Cleanup(func() { cleanup(rdb, prefix) })
		return redisstore.New(rdb, redisstore.WithPrefix(prefix))
	})
}
