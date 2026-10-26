package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/seed"
	"github.com/joalen/lattice/internal/setstore/mem"
	redisstore "github.com/joalen/lattice/internal/setstore/redis"
)

type config struct {
	redisAddr string
	prefix    string
	out       string
	sizes     []int
	backends  []string
	iters     int
	minSample int
	budget    time.Duration
	seed      int64
	micro     bool
}

func main() {
	var cfg config
	var sizes, backends string
	flag.StringVar(&cfg.redisAddr, "redis", "localhost:6379", "redis address")
	flag.StringVar(&cfg.prefix, "prefix", "lattice-bench:", "key prefix; everything under it is deleted before and after each run")
	flag.StringVar(&cfg.out, "out", "", "also write the report to this file")
	flag.StringVar(&sizes, "sizes", "300,10000,100000", "dataset sizes")
	flag.StringVar(&backends, "backends", "redis,mem", "backends to run")
	flag.IntVar(&cfg.iters, "iters", 300, "maximum samples per operation")
	flag.IntVar(&cfg.minSample, "min", 30, "minimum samples per operation, even past the time budget")
	flag.DurationVar(&cfg.budget, "budget", 5*time.Second, "time budget per operation")
	flag.Int64Var(&cfg.seed, "seed", 1, "dataset and query seed")
	flag.BoolVar(&cfg.micro, "micro", true, "run the Redis command comparisons")
	flag.Parse()

	for _, s := range strings.Split(sizes, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			fatalf("bad size %q", s)
		}
		cfg.sizes = append(cfg.sizes, n)
	}
	cfg.backends = strings.Split(backends, ",")

	report, failed := run(cfg)
	fmt.Print(report)
	if cfg.out != "" {
		if err := os.WriteFile(cfg.out, []byte(report), 0o644); err != nil {
			fatalf("write %s: %v", cfg.out, err)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench: "+format+"\n", args...)
	os.Exit(2)
}

type backend struct {
	name   string
	store  ports.SetStore
	rdb    *goredis.Client
	trips  *tripCounter
	prefix string
}

func (b *backend) close() {
	if b.rdb != nil {
		flushPrefix(b.rdb, b.prefix)
		_ = b.rdb.Close()
	}
}

func openBackend(cfg config, name string, size int) (*backend, error) {
	switch name {
	case "mem":
		return &backend{name: name, store: mem.New()}, nil
	case "redis":
		rdb := goredis.NewClient(&goredis.Options{Addr: cfg.redisAddr})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			_ = rdb.Close()
			return nil, fmt.Errorf("cannot reach redis at %s: %w", cfg.redisAddr, err)
		}
		trips := &tripCounter{}
		rdb.AddHook(trips)
		prefix := fmt.Sprintf("%s%d:", cfg.prefix, size)
		flushPrefix(rdb, prefix)
		return &backend{name: name, store: redisstore.New(rdb, redisstore.WithPrefix(prefix)), rdb: rdb, trips: trips, prefix: prefix}, nil
	}
	return nil, fmt.Errorf("unknown backend %q", name)
}

func flushPrefix(rdb *goredis.Client, prefix string) {
	ctx := context.Background()
	pipe := rdb.Pipeline()
	pending := 0
	it := rdb.Scan(ctx, 0, prefix+"*", 1000).Iterator()
	for it.Next(ctx) {
		pipe.Unlink(ctx, it.Val())
		if pending++; pending == 1000 {
			_, _ = pipe.Exec(ctx)
			pending = 0
		}
	}
	if pending > 0 {
		_, _ = pipe.Exec(ctx)
	}
}

type onceResult struct {
	Name  string
	D     time.Duration
	Trips int64
	Note  string
}

func run(cfg config) (string, bool) {
	var sb strings.Builder
	failed := false
	writeHeader(&sb, cfg)

	for _, name := range cfg.backends {
		for _, size := range cfg.sizes {
			fmt.Fprintf(os.Stderr, "bench: %s, %d documents\n", name, size)
			be, err := openBackend(cfg, name, size)
			if err != nil {
				fmt.Fprintf(&sb, "\n## %s, %d documents\n\nskipped: %v\n", name, size, err)
				failed = true
				continue
			}
			if err := runSize(&sb, cfg, be, size); err != nil {
				fmt.Fprintf(&sb, "\nerror: %v\n", err)
				failed = true
			}
			be.close()
		}
	}
	return sb.String(), failed
}

func runSize(sb *strings.Builder, cfg config, be *backend, size int) error {
	ctx := context.Background()
	fmt.Fprintf(sb, "\n## %s, %d documents\n", be.name, size)

	items := seed.Items(size, cfg.seed)
	builder, err := ingest.NewBuilder(be.store, seed.Mapping())
	if err != nil {
		return err
	}

	timed := func(name string, docs []json.RawMessage, wantUpdated int) (onceResult, error) {
		before, t0 := be.trips.now(), time.Now()
		rep, err := builder.Ingest(ctx, docs, ingest.Options{})
		res := onceResult{Name: name, D: time.Since(t0), Trips: be.trips.now() - before}
		if err != nil {
			return res, err
		}
		res.Note = fmt.Sprintf("created %d, updated %d, unchanged %d", rep.Created, rep.Updated, rep.Unchanged)
		if wantUpdated >= 0 && rep.Updated != wantUpdated {
			return res, fmt.Errorf("%s: updated %d documents, want %d", name, rep.Updated, wantUpdated)
		}
		return res, nil
	}

	changed := append([]seed.Item(nil), items...)
	for i := 99; i < len(changed); i += 100 {
		changed[i].Score = (changed[i].Score + 1) % (seed.MaxScore + 1)
	}

	var once []onceResult
	for _, step := range []struct {
		name        string
		docs        []json.RawMessage
		wantUpdated int
	}{
		{"ingest, cold", seed.JSON(items), 0},
		{"re-ingest, nothing changed", seed.JSON(items), 0},
		{"re-ingest, 1% of documents changed", seed.JSON(changed), len(items) / 100},
	} {
		res, err := timed(step.name, step.docs, step.wantUpdated)
		once = append(once, res)
		if err != nil {
			writeOnce(sb, once, size)
			return err
		}
	}
	writeOnce(sb, once, size)

	if be.rdb != nil {
		writeRedisFacts(ctx, sb, be.rdb, be.prefix)
	}

	r := runner{iters: cfg.iters, minSamples: cfg.minSample, budget: cfg.budget, trips: be.trips, seed: cfg.seed}
	var results []opResult
	for _, sc := range scenarios(ctx, be.store, items) {
		fmt.Fprintf(os.Stderr, "  %s\n", sc.name)
		results = append(results, r.measure(sc.name, sc.run))
	}
	writeOps(sb, results, be.rdb != nil)

	if be.rdb != nil && cfg.micro {
		groups, err := microBenchmarks(ctx, be.rdb, be.prefix, be.store, r)
		if err != nil {
			return err
		}
		for _, g := range groups {
			writeMicro(sb, g)
		}
	}

	if be.rdb != nil {
		left, _ := be.rdb.Keys(ctx, be.prefix+"*_tmp*").Result()
		fmt.Fprintf(sb, "\nTemporary keys left behind after the run: %d\n", len(left))
	}
	return nil
}

func writeHeader(sb *strings.Builder, cfg config) {
	fmt.Fprintf(sb, "# Lattice benchmark results\n\n")
	fmt.Fprintf(sb, "Generated %s by `go run ./bench`.\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(sb, "- Go %s on %s/%s, %d CPUs%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), cpuModel())
	if hasBackend(cfg, "redis") {
		fmt.Fprintf(sb, "- Redis %s, same machine over loopback, so network latency is close to zero\n", redisVersion(cfg))
	}
	fmt.Fprintf(sb, "- Synthetic catalog from `internal/seed` (seed %d); every query picks its parameters from real documents\n", cfg.seed)
	fmt.Fprintf(sb, "- Up to %d samples per operation (at least %d, time budget %s), after %d warm-up runs\n", cfg.iters, cfg.minSample, cfg.budget, warmup)
	fmt.Fprintf(sb, "- \"matches\" is the median number of documents the query matched; \"round trips\" counts network round trips to Redis (a pipeline or transaction is one)\n")
}

func hasBackend(cfg config, name string) bool {
	for _, b := range cfg.backends {
		if b == name {
			return true
		}
	}
	return false
}

func cpuModel() string {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
			return " (" + strings.TrimSpace(v) + ")"
		}
	}
	return ""
}

func redisVersion(cfg config) string {
	rdb := goredis.NewClient(&goredis.Options{Addr: cfg.redisAddr})
	defer rdb.Close()
	info, err := rdb.Info(context.Background(), "server").Result()
	if err != nil {
		return "(unreachable)"
	}
	return infoField(info, "redis_version")
}

func infoField(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == field {
			return v
		}
	}
	return "?"
}

func writeRedisFacts(ctx context.Context, sb *strings.Builder, rdb *goredis.Client, prefix string) {
	mem, _ := rdb.Info(ctx, "memory").Result()
	used, _ := strconv.ParseFloat(infoField(mem, "used_memory"), 64)
	keys, _ := rdb.DBSize(ctx).Result()
	fmt.Fprintf(sb, "\nRedis holds %d keys and uses %.1f MB after ingestion.\n", keys, used/1e6)
}

func writeOnce(sb *strings.Builder, rows []onceResult, size int) {
	fmt.Fprintf(sb, "\n| ingestion | time | docs/s | round trips | result |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		trips := fmt.Sprint(r.Trips)
		if r.Trips == 0 {
			trips = "–"
		}
		fmt.Fprintf(sb, "| %s | %s | %.0f | %s | %s |\n", r.Name, fmtDur(r.D), float64(size)/r.D.Seconds(), trips, r.Note)
	}
}

func writeOps(sb *strings.Builder, rows []opResult, withTrips bool) {
	fmt.Fprintf(sb, "\n| operation | matches | p50 | p95 | p99 | round trips | samples |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		if r.Err != nil {
			fmt.Fprintf(sb, "| %s | error: %v | | | | | |\n", r.Name, r.Err)
			continue
		}
		trips := "–"
		if withTrips {
			trips = fmt.Sprint(r.Trips)
		}
		fmt.Fprintf(sb, "| %s | %d | %s | %s | %s | %s | %d |\n", r.Name, r.Matches, fmtDur(r.P50), fmtDur(r.P95), fmtDur(r.P99), trips, r.Samples)
	}
}

func writeMicro(sb *strings.Builder, g microGroup) {
	fmt.Fprintf(sb, "\n### %s\n\n%s\n\n| approach | result | p50 | p95 | p99 | round trips |\n|---|---|---|---|---|---|\n", g.Title, g.Note)
	for _, r := range g.Rows {
		if r.Err != nil {
			fmt.Fprintf(sb, "| %s | error: %v | | | | |\n", r.Name, r.Err)
			continue
		}
		fmt.Fprintf(sb, "| %s | %d | %s | %s | %s | %d |\n", r.Name, r.Matches, fmtDur(r.P50), fmtDur(r.P95), fmtDur(r.P99), r.Trips)
	}
}
