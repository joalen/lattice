package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/cli"
	"github.com/joalen/lattice/internal/ports"
	redisstore "github.com/joalen/lattice/internal/setstore/redis"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, openRedis)
	stop()
	os.Exit(code)
}

func openRedis(addr, prefix string) (ports.SetStore, func() error, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, nil, fmt.Errorf("cannot reach redis at %s: %w", addr, err)
	}
	return redisstore.New(rdb, redisstore.WithPrefix(prefix)), rdb.Close, nil
}
