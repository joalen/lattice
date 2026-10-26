package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

type Options struct {
	// Prune removes indexed documents missing from this run; leave off for partial ingests.
	Prune bool
}

type Report struct {
	Docs        int `json:"docs"`
	Created     int `json:"created"`
	Updated     int `json:"updated"`
	Unchanged   int `json:"unchanged"`
	Pruned      int `json:"pruned"`
	KeysAdded   int `json:"keys_added"`
	KeysRemoved int `json:"keys_removed"`
}

type Builder struct {
	store      ports.SetStore
	m          Mapping
	setPaths   []string
	rangePaths []string
}

func NewBuilder(store ports.SetStore, m Mapping) (*Builder, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	b := &Builder{store: store, m: m}
	for path, typ := range m.Fields {
		switch typ {
		case IndexSet:
			b.setPaths = append(b.setPaths, path)
		case IndexRange:
			b.rangePaths = append(b.rangePaths, path)
		}
	}
	sort.Strings(b.setPaths)
	sort.Strings(b.rangePaths)
	return b, nil
}

type prepared struct {
	id     string
	raw    []byte
	keys   []string
	scores map[string]float64
}

// Ingest validates every document before writing any, then writes each as an atomic batch.
func (b *Builder) Ingest(docs []json.RawMessage, opts Options) (Report, error) {
	rep := Report{Docs: len(docs)}

	items := make([]prepared, 0, len(docs))
	seen := make(map[string]int, len(docs))

	for i, raw := range docs {
		flat, err := Flatten(raw)
		if err != nil {
			return rep, fmt.Errorf("ingest: document %d: %w", i, err)
		}
		id, err := b.idOf(flat)
		if err != nil {
			return rep, fmt.Errorf("ingest: document %d: %w", i, err)
		}
		if first, dup := seen[id]; dup {
			return rep, fmt.Errorf("ingest: duplicate id %q (documents %d and %d)", id, first, i)
		}
		seen[id] = i
		keys, scores, err := b.keysFor(flat)
		if err != nil {
			return rep, fmt.Errorf("ingest: document %d: %w", i, err)
		}
		items = append(items, prepared{id: id, raw: raw, keys: keys, scores: scores})
	}

	if err := SaveMapping(b.store, b.m); err != nil {
		return rep, fmt.Errorf("ingest: save mapping: %w", err)
	}

	for _, it := range items {
		if err := b.write(it, &rep); err != nil {
			return rep, err
		}
	}

	if opts.Prune {
		if err := b.prune(seen, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (b *Builder) idOf(flat map[string]Field) (string, error) {
	f, ok := flat[b.m.ID]
	if !ok || f.Array || len(f.Values) != 1 {
		return "", fmt.Errorf("no single scalar id at %q", b.m.ID)
	}
	id := strings.TrimSpace(f.Values[0].Text)
	if id == "" {
		return "", fmt.Errorf("empty id at %q", b.m.ID)
	}
	return id, nil
}

func (b *Builder) keysFor(flat map[string]Field) ([]string, map[string]float64, error) {
	set := map[string]struct{}{keyspace.Universe(b.m.Entity): {}}
	for _, path := range b.setPaths {
		for _, v := range flat[path].Values {
			if keyspace.Normalize(v.Text) == "" {
				continue
			}
			set[keyspace.Facet(b.m.Entity, path, v.Text)] = struct{}{}
		}
	}

	var scores map[string]float64
	for _, path := range b.rangePaths {
		f, ok := flat[path]
		if !ok {
			continue
		}
		if f.Array || len(f.Values) != 1 || f.Values[0].Kind != KindNumber {
			return nil, nil, fmt.Errorf("range field %q must hold a single number", path)
		}
		score, err := strconv.ParseFloat(f.Values[0].Text, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("range field %q: %w", path, err)
		}
		key := keyspace.Range(b.m.Entity, path)
		if scores == nil {
			scores = make(map[string]float64)
		}
		scores[key] = score
		set[key] = struct{}{}
	}
	return sortedKeys(set), scores, nil
}

func (b *Builder) write(it prepared, rep *Report) error {
	entity := b.m.Entity
	rev := keyspace.Memberships(entity, it.id)

	oldList, err := b.store.Members(rev)
	if err != nil {
		return fmt.Errorf("ingest: %s: read memberships: %w", it.id, err)
	}
	oldKeys := toSet(oldList)
	newKeys := toSet(it.keys)

	added := difference(it.keys, oldKeys)
	removed := difference(oldList, newKeys)

	existing, err := b.store.GetDocument(entity, it.id)
	created := errors.Is(err, ports.ErrNotFound)
	if err != nil && !created {
		return fmt.Errorf("ingest: %s: read document: %w", it.id, err)
	}

	if !created && bytes.Equal(existing, it.raw) && len(added) == 0 && len(removed) == 0 {
		rep.Unchanged++
		return nil
	}

	ops := make([]ports.Op, 0, 3+len(added)+len(removed)+len(it.scores))
	ops = append(ops, ports.PutDoc(entity, it.id, it.raw))
	for _, k := range removed {
		ops = append(ops, b.dropFrom(k, it.id))
	}
	for _, k := range added {
		if !keyspace.IsRange(entity, k) {
			ops = append(ops, ports.Add(k, it.id))
		}
	}
	for _, k := range it.keys {
		if score, ok := it.scores[k]; ok {
			ops = append(ops, ports.SetScore(k, it.id, score))
		}
	}
	ops = append(ops, ports.Remove(rev, removed...), ports.Add(rev, added...))

	if err := b.store.Apply(ops...); err != nil {
		return fmt.Errorf("ingest: %s: apply: %w", it.id, err)
	}

	if created {
		rep.Created++
	} else {
		rep.Updated++
	}
	rep.KeysAdded += len(added)
	rep.KeysRemoved += len(removed)
	return nil
}

func (b *Builder) dropFrom(key, id string) ports.Op {
	if keyspace.IsRange(b.m.Entity, key) {
		return ports.DelScore(key, id)
	}
	return ports.Remove(key, id)
}

func (b *Builder) prune(seen map[string]int, rep *Report) error {
	entity := b.m.Entity

	ids, err := b.store.Members(keyspace.Universe(entity))
	if err != nil {
		return fmt.Errorf("ingest: prune: read universe: %w", err)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		rev := keyspace.Memberships(entity, id)
		memberOf, err := b.store.Members(rev)
		if err != nil {
			return fmt.Errorf("ingest: prune %s: %w", id, err)
		}

		ops := make([]ports.Op, 0, len(memberOf)+2)
		for _, k := range memberOf {
			ops = append(ops, b.dropFrom(k, id))
		}
		ops = append(ops, ports.DeleteKey(rev), ports.DelDoc(entity, id))

		if err := b.store.Apply(ops...); err != nil {
			return fmt.Errorf("ingest: prune %s: %w", id, err)
		}
		rep.Pruned++
		rep.KeysRemoved += len(memberOf)
	}
	return nil
}

func toSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		out[v] = struct{}{}
	}
	return out
}

func difference(list []string, exclude map[string]struct{}) []string {
	var out []string
	for _, v := range list {
		if _, skip := exclude[v]; !skip {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
