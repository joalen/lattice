package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/joalen/lattice/internal/keyspace"
	"github.com/joalen/lattice/internal/ports"
)

// Options controls one ingestion run.
type Options struct {
	// Prune removes documents that are in the index but absent from this run.
	Prune bool
}

// Report summarizes what a run changed.
type Report struct {
	Docs        int // documents read
	Created     int // not previously indexed
	Updated     int // indexed before, content or memberships changed
	Unchanged   int // nothing to do
	Pruned      int // removed because they disappeared from the source
	KeysAdded   int // facet memberships added
	KeysRemoved int // facet memberships removed

	// Deferred lists paths mapped as "range" that were seen but not indexed!
	Deferred []string
}

// Builder turns documents into index entries in any ports.SetStore.
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
	id   string
	raw  []byte
	keys []string // sorted facet keys including the universe
}

func (b *Builder) Ingest(docs []json.RawMessage, opts Options) (Report, error) {
	rep := Report{Docs: len(docs), Deferred: append([]string(nil), b.rangePaths...)}

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
		items = append(items, prepared{id: id, raw: raw, keys: b.keysFor(flat)})
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

func (b *Builder) keysFor(flat map[string]Field) []string {
	set := map[string]struct{}{keyspace.Universe(b.m.Entity): {}}
	for _, path := range b.setPaths {
		for _, v := range flat[path].Values {
			if keyspace.Normalize(v.Text) == "" {
				continue
			}
			set[keyspace.Facet(b.m.Entity, path, v.Text)] = struct{}{}
		}
	}
	return sortedKeys(set)
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

	ops := make([]ports.Op, 0, 3+len(added)+len(removed))
	ops = append(ops, ports.PutDoc(entity, it.id, it.raw))
	for _, k := range removed {
		ops = append(ops, ports.Remove(k, it.id))
	}
	for _, k := range added {
		ops = append(ops, ports.Add(k, it.id))
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
			ops = append(ops, ports.Remove(k, id))
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
