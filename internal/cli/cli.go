package cli

import (
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
func Run(args []string, stdout, stderr io.Writer, open OpenFunc) int {
	a := &app{out: stdout, err: stderr, open: open}

	if len(args) == 0 {
		a.usage(stderr)
		return 2
	}
	cmds := map[string]func([]string) error{
		"propose": a.propose,
		"ingest":  a.ingest,
		"search":  a.search,
		"get":     a.get,
		"similar": a.similar,
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

	err := fn(args[1:])
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
  lattice propose --entity NAME [--id PATH] PATH...
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

func (a *app) propose(args []string) error {
	fs := a.flagSet("propose")
	entity := fs.String("entity", "", "entity name, used as the key namespace (required)")
	id := fs.String("id", "", "dot-path of the document ID (default: guess)")
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
	m, err := ingest.ProposeMapping(*entity, docs, ingest.ProposeOptions{ID: *id})
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

func (a *app) ingest(args []string) error {
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
	rep, err := b.Ingest(docs, ingest.Options{Prune: *prune})
	if err != nil {
		return err
	}
	return a.emit(rep, *compact)
}

type resultItem struct {
	ID      string         `json:"id"`
	Summary map[string]any `json:"summary"`
}

type searchOutput struct {
	Total   int                       `json:"total"`
	Offset  int                       `json:"offset"`
	Results []resultItem              `json:"results"`
	Facets  map[string][]facets.Count `json:"facets,omitempty"`
}

func (a *app) search(args []string) error {
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

	m, err := a.mapping(store, q.Entity)
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

	ids, err := search.Match(store, q)
	if err != nil {
		return err
	}
	page, err := search.Page(store, q, ids)
	if err != nil {
		return err
	}

	out := searchOutput{Total: page.Total, Offset: q.Offset, Results: make([]resultItem, 0, len(page.IDs))}
	summaryFields := splitList(*fields)
	for _, id := range page.IDs {
		sum, err := summarize(store, q.Entity, id, summaryFields)
		if err != nil {
			return err
		}
		out.Results = append(out.Results, resultItem{ID: id, Summary: sum})
	}
	if len(facetList) > 0 {
		out.Facets, err = facets.Counts(store, q.Entity, facetList, ids)
		if err != nil {
			return err
		}
	}
	return a.emit(out, *compact)
}

func (a *app) get(args []string) error {
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

	raw, err := store.GetDocument(*entity, id)
	if errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("no document %q in entity %q", id, *entity)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.out, "%s\n", raw)
	return err
}

type similarItem struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Shared  int            `json:"shared"`
	Summary map[string]any `json:"summary"`
}

func (a *app) similar(args []string) error {
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

	m, err := a.mapping(store, *entity)
	if err != nil {
		return err
	}
	pathList := splitList(*paths)
	for _, p := range pathList {
		if err := checkField(m, p, ingest.IndexSet); err != nil {
			return err
		}
	}

	matches, err := similarity.Similar(store, *entity, id, similarity.Options{Paths: pathList, Limit: *limit})
	if errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("no document %q in entity %q", id, *entity)
	}
	if err != nil {
		return err
	}

	summaryFields := splitList(*fields)
	items := make([]similarItem, 0, len(matches))
	for _, m := range matches {
		sum, err := summarize(store, *entity, m.ID, summaryFields)
		if err != nil {
			return err
		}
		items = append(items, similarItem{ID: m.ID, Score: m.Score, Shared: m.Shared, Summary: sum})
	}
	return a.emit(struct {
		ID      string        `json:"id"`
		Matches []similarItem `json:"matches"`
	}{id, items}, *compact)
}

func summarize(store ports.SetStore, entity, id string, fields []string) (map[string]any, error) {
	raw, err := store.GetDocument(entity, id)
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

func (a *app) mapping(store ports.SetStore, entity string) (ingest.Mapping, error) {
	m, err := ingest.LoadMapping(store, entity)
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
