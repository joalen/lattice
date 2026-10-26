package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/joalen/lattice/internal/facets"
	"github.com/joalen/lattice/internal/ingest"
	"github.com/joalen/lattice/internal/ports"
	"github.com/joalen/lattice/internal/search"
	"github.com/joalen/lattice/internal/similarity"
	"github.com/joalen/lattice/internal/wire"
)

const maxSummaryRunes = 120

// OpenFunc connects to a store. cmd/lattice supplies Redis; tests supply mem.
type OpenFunc func(addr, prefix string) (ports.SetStore, func() error, error)

type app struct {
	out, err io.Writer
	open     OpenFunc
}

type usageError string

func (e usageError) Error() string { return string(e) }

var (
	errHelp     = errors.New("help requested")
	errReported = errors.New("flag error already reported")
)

// Run executes one command and returns the process exit code: 0 on success,
// 1 on a runtime failure, 2 on bad usage.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, open OpenFunc) int {
	a := &app{out: stdout, err: stderr, open: open}

	if len(args) == 0 {
		a.usage(stderr)
		return 2
	}
	cmds := map[string]func(context.Context, []string) error{
		"discover": a.discover,
		"propose":  a.propose,
		"ingest":   a.ingest,
		"search":   a.search,
		"get":      a.get,
		"similar":  a.similar,
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		a.usage(stdout)
		return 0
	}
	fn, ok := cmds[name]
	if !ok {
		fmt.Fprintf(stderr, "lattice: unknown command %q\n\n", name)
		a.usage(stderr)
		return 2
	}

	err := fn(ctx, args[1:])
	var ue usageError
	switch {
	case err == nil, errors.Is(err, errHelp):
		return 0
	case errors.Is(err, errReported):
		return 2
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "lattice %s: %v\n", name, err)
		return 2
	}
	fmt.Fprintf(stderr, "lattice %s: %v\n", name, err)
	return 1
}

func (a *app) usage(w io.Writer) {
	fmt.Fprint(w, `lattice: set-theory search over JSON documents

Usage:
  lattice discover [--clusters] [--limit N] PATH...
  lattice propose --entity NAME [--id PATH] [--max-categorical N] PATH...
  lattice ingest  --mapping FILE [--prune] PATH...
  lattice search  --entity NAME [--facet path=a,b]... [--not path=a]... [--range path=lo..hi]...
                  [--sort path[:desc]] [--limit N] [--offset N] [--facets p1,p2] [--fields p1,p2]
  lattice get     --entity NAME ID
  lattice similar --entity NAME [--paths p1,p2] [--limit N] ID

PATH is a JSON file or a directory of them. Flags go before positional arguments.
Commands that touch the index also take --redis ADDR (env LATTICE_REDIS_ADDR,
default localhost:6379) and --prefix (env LATTICE_PREFIX). Output is JSON;
add --compact for one line.
`)
}

type conn struct{ addr, prefix string }

func (a *app) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("lattice "+name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	return fs
}

func connFlags(fs *flag.FlagSet, c *conn) {
	addr := os.Getenv("LATTICE_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	fs.StringVar(&c.addr, "redis", addr, "redis address")
	fs.StringVar(&c.prefix, "prefix", os.Getenv("LATTICE_PREFIX"), "key prefix for everything lattice writes")
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelp
		}
		return errReported
	}
	return nil
}

func (a *app) connect(c conn) (ports.SetStore, func(), error) {
	store, closeFn, err := a.open(c.addr, c.prefix)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { _ = closeFn() }, nil
}

func (a *app) emit(v any, compact bool) error {
	enc := json.NewEncoder(a.out)
	enc.SetEscapeHTML(false)
	if !compact {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

func (a *app) discover(_ context.Context, args []string) error {
	fs := a.flagSet("discover")
	clusters := fs.Bool("clusters", false, "merge near-identical shapes into recurring types")
	limit := fs.Int("limit", 20, "entries to show; 0 for all")
	compact := fs.Bool("compact", false, "single-line JSON output")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return usageError("at least one PATH is required")
	}
	if *limit < 0 {
		return usageError("--limit must not be negative")
	}

	docs, err := loadAll(fs.Args())
	if err != nil {
		return err
	}
	raws := make([][]byte, len(docs))
	for i, d := range docs {
		raws[i] = d
	}

	var freq map[string]int
	if *clusters {
		freq, err = ingest.DiscoverClusters(raws)
	} else {
		freq, err = ingest.DiscoverCorpus(raws)
	}
	if err != nil {
		return err
	}

	entries := make([]wire.DiscoverEntry, 0, len(freq))
	for sig, n := range freq {
		entries = append(entries, wire.DiscoverEntry{Signature: sig, Count: n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Signature < entries[j].Signature
	})
	if *limit > 0 && *limit < len(entries) {
		entries = entries[:*limit]
	}
	return a.emit(entries, *compact)
}

func (a *app) propose(_ context.Context, args []string) error {
	fs := a.flagSet("propose")
	entity := fs.String("entity", "", "entity name, used as the key namespace (required)")
	id := fs.String("id", "", "dot-path of the document ID (default: guess)")
	maxCat := fs.Int("max-categorical", 30, "most distinct values a string field may have and still be proposed as a facet")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *entity == "" {
		return usageError("--entity is required")
	}
	if fs.NArg() == 0 {
		return usageError("at least one PATH is required")
	}

	docs, err := loadAll(fs.Args())
	if err != nil {
		return err
	}
	m, err := ingest.ProposeMapping(*entity, docs, ingest.ProposeOptions{ID: *id, MaxCategorical: *maxCat})
	if err != nil {
		return err
	}
	data, err := m.JSON()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.out, "%s\n", data)
	return err
}

func (a *app) ingest(ctx context.Context, args []string) error {
	var c conn
	fs := a.flagSet("ingest")
	connFlags(fs, &c)
	mappingPath := fs.String("mapping", "", "mapping file from `lattice propose` (required)")
	prune := fs.Bool("prune", false, "remove indexed documents missing from PATH")
	compact := fs.Bool("compact", false, "single-line JSON output")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *mappingPath == "" {
		return usageError("--mapping is required")
	}
	if fs.NArg() == 0 {
		return usageError("at least one PATH is required")
	}

	f, err := os.Open(*mappingPath)
	if err != nil {
		return err
	}
	m, err := ingest.ParseMapping(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", *mappingPath, err)
	}
	docs, err := loadAll(fs.Args())
	if err != nil {
		return err
	}

	store, done, err := a.connect(c)
	if err != nil {
		return err
	}
	defer done()

	b, err := ingest.NewBuilder(store, m)
	if err != nil {
		return err
	}
	rep, err := b.Ingest(ctx, docs, ingest.Options{Prune: *prune})
	if err != nil {
		return err
	}
	return a.emit(ingestReport(rep), *compact)
}

func (a *app) search(ctx context.Context, args []string) error {
	var c conn
	var facetFlags, notFlags, rangeFlags multiFlag
	fs := a.flagSet("search")
	connFlags(fs, &c)
	entity := fs.String("entity", "", "entity to search (required)")
	fs.Var(&facetFlags, "facet", "path=v1,v2: match any listed value; repeat to AND fields")
	fs.Var(&notFlags, "not", "path=v1,v2: exclude documents with any listed value; repeatable")
	fs.Var(&rangeFlags, "range", "path=lo..hi: numeric range, either bound optional; repeatable")
	sortSpec := fs.String("sort", "", "numeric field, optionally :desc")
	limit := fs.Int("limit", 20, "page size; 0 for no limit")
	offset := fs.Int("offset", 0, "results to skip")
	facetPaths := fs.String("facets", "", "comma-separated fields to count across all matches")
	fields := fs.String("fields", "", "comma-separated fields in each summary (default: top-level scalars)")
	compact := fs.Bool("compact", false, "single-line JSON output")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *entity == "" {
		return usageError("--entity is required")
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("unexpected arguments %q (flags go before positional arguments)", fs.Args()))
	}
	if *limit < 0 || *offset < 0 {
		return usageError("--limit and --offset must not be negative")
	}

	q := search.Query{Entity: *entity, Limit: *limit, Offset: *offset}
	for _, s := range facetFlags {
		f, err := parseFacet(s)
		if err != nil {
			return err
		}
		q.Facets = append(q.Facets, f)
	}
	for _, s := range notFlags {
		f, err := parseFacet(s)
		if err != nil {
			return err
		}
		q.Not = append(q.Not, f)
	}
	for _, s := range rangeFlags {
		r, err := parseRange(s)
		if err != nil {
			return err
		}
		q.Ranges = append(q.Ranges, r)
	}
	if *sortSpec != "" {
		srt, err := parseSort(*sortSpec)
		if err != nil {
			return err
		}
		q.Sort = srt
	}

	store, done, err := a.connect(c)
	if err != nil {
		return err
	}
	defer done()

	m, err := a.mapping(ctx, store, q.Entity)
	if err != nil {
		return err
	}
	facetList := splitList(*facetPaths)
	for _, f := range append(slices.Clone(q.Facets), q.Not...) {
		if err := checkField(m, f.Path, ingest.IndexSet); err != nil {
			return err
		}
	}
	for _, p := range facetList {
		if err := checkField(m, p, ingest.IndexSet); err != nil {
			return err
		}
	}
	for _, r := range q.Ranges {
		if err := checkField(m, r.Path, ingest.IndexRange); err != nil {
			return err
		}
	}
	if q.Sort != nil {
		if err := checkField(m, q.Sort.Path, ingest.IndexRange); err != nil {
			return err
		}
	}

	ids, err := search.Match(ctx, store, q)
	if err != nil {
		return err
	}
	page, err := search.Page(ctx, store, q, ids)
	if err != nil {
		return err
	}

	out := wire.SearchResponse{Total: page.Total, Offset: q.Offset, Results: make([]wire.SearchHit, 0, len(page.IDs))}
	summaryFields := splitList(*fields)
	for _, id := range page.IDs {
		sum, err := summarize(ctx, store, q.Entity, id, summaryFields)
		if err != nil {
			return err
		}
		out.Results = append(out.Results, wire.SearchHit{ID: id, Summary: sum})
	}
	if len(facetList) > 0 {
		counts, err := facets.Counts(ctx, store, q.Entity, facetList, ids)
		if err != nil {
			return err
		}
		out.Facets = facetCounts(counts)
	}
	return a.emit(out, *compact)
}

func (a *app) get(ctx context.Context, args []string) error {
	var c conn
	fs := a.flagSet("get")
	connFlags(fs, &c)
	entity := fs.String("entity", "", "entity (required)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *entity == "" {
		return usageError("--entity is required")
	}
	if fs.NArg() != 1 {
		return usageError("exactly one ID is required")
	}
	id := fs.Arg(0)

	store, done, err := a.connect(c)
	if err != nil {
		return err
	}
	defer done()

	raw, err := store.GetDocument(ctx, *entity, id)
	if errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("no document %q in entity %q", id, *entity)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.out, "%s\n", raw)
	return err
}

func (a *app) similar(ctx context.Context, args []string) error {
	var c conn
	fs := a.flagSet("similar")
	connFlags(fs, &c)
	entity := fs.String("entity", "", "entity (required)")
	paths := fs.String("paths", "", "comma-separated fields to compare (default: all facets)")
	limit := fs.Int("limit", 10, "matches to return; 0 for all")
	fields := fs.String("fields", "", "comma-separated fields in each summary (default: top-level scalars)")
	compact := fs.Bool("compact", false, "single-line JSON output")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *entity == "" {
		return usageError("--entity is required")
	}
	if fs.NArg() != 1 {
		return usageError("exactly one ID is required")
	}
	if *limit < 0 {
		return usageError("--limit must not be negative")
	}
	id := fs.Arg(0)

	store, done, err := a.connect(c)
	if err != nil {
		return err
	}
	defer done()

	m, err := a.mapping(ctx, store, *entity)
	if err != nil {
		return err
	}
	pathList := splitList(*paths)
	for _, p := range pathList {
		if err := checkField(m, p, ingest.IndexSet); err != nil {
			return err
		}
	}

	matches, err := similarity.Similar(ctx, store, *entity, id, similarity.Options{Paths: pathList, Limit: *limit})
	if errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("no document %q in entity %q", id, *entity)
	}
	if err != nil {
		return err
	}

	summaryFields := splitList(*fields)
	out := wire.SimilarResponse{ID: id, Matches: make([]wire.SimilarMatch, 0, len(matches))}
	for _, m := range matches {
		sum, err := summarize(ctx, store, *entity, m.ID, summaryFields)
		if err != nil {
			return err
		}
		out.Matches = append(out.Matches, wire.SimilarMatch{ID: m.ID, Score: m.Score, Shared: m.Shared, Summary: sum})
	}
	return a.emit(out, *compact)
}

func ingestReport(r ingest.Report) wire.IngestReport {
	return wire.IngestReport{
		Docs:        r.Docs,
		Created:     r.Created,
		Updated:     r.Updated,
		Unchanged:   r.Unchanged,
		Pruned:      r.Pruned,
		KeysAdded:   r.KeysAdded,
		KeysRemoved: r.KeysRemoved,
	}
}

func facetCounts(in map[string][]facets.Count) map[string][]wire.FacetCount {
	out := make(map[string][]wire.FacetCount, len(in))
	for path, counts := range in {
		converted := make([]wire.FacetCount, len(counts))
		for i, c := range counts {
			converted[i] = wire.FacetCount{Value: c.Value, Count: c.N}
		}
		out[path] = converted
	}
	return out
}

func summarize(ctx context.Context, store ports.SetStore, entity, id string, fields []string) (map[string]any, error) {
	raw, err := store.GetDocument(ctx, entity, id)
	if err != nil {
		return nil, fmt.Errorf("document %q: %w", id, err)
	}
	flat, err := ingest.Flatten(raw)
	if err != nil {
		return nil, fmt.Errorf("document %q: %w", id, err)
	}

	out := make(map[string]any)
	if len(fields) == 0 {
		for path, f := range flat {
			if !strings.Contains(path, ".") && !f.Array && len(f.Values) == 1 {
				out[path] = scalarValue(f.Values[0])
			}
		}
		return out, nil
	}
	for _, path := range fields {
		f, ok := flat[path]
		if !ok {
			continue
		}
		if !f.Array {
			out[path] = scalarValue(f.Values[0])
			continue
		}
		vals := make([]any, len(f.Values))
		for i, v := range f.Values {
			vals[i] = scalarValue(v)
		}
		out[path] = vals
	}
	return out, nil
}

func scalarValue(s ingest.Scalar) any {
	switch s.Kind {
	case ingest.KindNumber:
		return json.Number(s.Text)
	case ingest.KindBool:
		return s.Text == "true"
	}
	if utf8.RuneCountInString(s.Text) > maxSummaryRunes {
		return string([]rune(s.Text)[:maxSummaryRunes]) + "…"
	}
	return s.Text
}

func (a *app) mapping(ctx context.Context, store ports.SetStore, entity string) (ingest.Mapping, error) {
	m, err := ingest.LoadMapping(ctx, store, entity)
	if errors.Is(err, ports.ErrNotFound) {
		return ingest.Mapping{}, fmt.Errorf("entity %q has not been ingested", entity)
	}
	return m, err
}

func checkField(m ingest.Mapping, path string, want ingest.IndexType) error {
	got, ok := m.Fields[path]
	if ok && got == want {
		return nil
	}

	var have []string
	for p, t := range m.Fields {
		if t == want {
			have = append(have, p)
		}
	}
	sort.Strings(have)
	label, list := "facet fields", "none"
	if want == ingest.IndexRange {
		label = "numeric fields"
	}
	if len(have) > 0 {
		list = strings.Join(have, ", ")
	}

	if !ok {
		return usageError(fmt.Sprintf("unknown field %q (%s: %s)", path, label, list))
	}
	return usageError(fmt.Sprintf("field %q is mapped as %q, not %q; change the mapping and re-ingest (%s: %s)", path, got, want, label, list))
}

func loadAll(paths []string) ([]json.RawMessage, error) {
	var docs []json.RawMessage
	for _, p := range paths {
		found, err := ingest.LoadPath(p)
		if err != nil {
			return nil, err
		}
		docs = append(docs, found...)
	}
	return docs, nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, " ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseFacet(s string) (search.Facet, error) {
	path, rest, ok := strings.Cut(s, "=")
	path = strings.TrimSpace(path)
	values := splitList(rest)
	if !ok || path == "" || len(values) == 0 {
		return search.Facet{}, usageError(fmt.Sprintf("bad facet %q: want path=value[,value...]", s))
	}
	return search.Facet{Path: path, Values: values}, nil
}

func parseRange(s string) (search.Range, error) {
	bad := usageError(fmt.Sprintf("bad range %q: want path=lo..hi with at least one bound", s))
	path, rest, ok := strings.Cut(s, "=")
	lo, hi, dots := strings.Cut(rest, "..")
	path = strings.TrimSpace(path)
	if !ok || !dots || path == "" {
		return search.Range{}, bad
	}

	r := search.Range{Path: path}
	for _, b := range []struct {
		text string
		dst  **float64
	}{{lo, &r.Min}, {hi, &r.Max}} {
		if b.text = strings.TrimSpace(b.text); b.text == "" {
			continue
		}
		v, err := strconv.ParseFloat(b.text, 64)
		if err != nil || math.IsNaN(v) {
			return search.Range{}, bad
		}
		*b.dst = &v
	}
	switch {
	case r.Min == nil && r.Max == nil:
		return search.Range{}, bad
	case r.Min != nil && r.Max != nil && *r.Min > *r.Max:
		return search.Range{}, usageError(fmt.Sprintf("bad range %q: lower bound is above upper bound", s))
	}
	return r, nil
}

func parseSort(s string) (*search.Sort, error) {
	path, dir, _ := strings.Cut(s, ":")
	path = strings.TrimSpace(path)
	if path == "" || (dir != "" && dir != "asc" && dir != "desc") {
		return nil, usageError(fmt.Sprintf("bad sort %q: want path or path:desc", s))
	}
	return &search.Sort{Path: path, Desc: dir == "desc"}, nil
}
