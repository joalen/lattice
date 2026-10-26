# How set theory maps onto Lattice

Every filter in Lattice is a set of document IDs, and every query combines those sets. This page lists each idea, the Redis command it becomes, and where it lives in the code.

## The mapping

| Set theory | In Lattice | Redis | Code |
|---|---|---|---|
| A set | The IDs of documents sharing a field value | `SADD item:tier:pro 17 42 …` | `ingest.Builder`, `keyspace.Facet` |
| The universe | Every ID of an entity | `item:universe` | `keyspace.Universe` |
| Union | OR within one field (`tier=free,pro`) | `SUNIONSTORE` into a temp key | `search.Match` |
| Intersection | AND across fields | `SINTER` | `search.Match` |
| Difference | NOT (`--not tier=free`) | `SDIFF` | `search.Match` |
| Complement | A NOT with no positive filter: universe minus the excluded sets | `SDIFF item:universe …` | `search.Match` |
| Cardinality | Facet counts: how many of the current results have each value | `SINTERCARD` | `facets.Counts` |
| Sorted set | A numeric field, for ranges and ordering | `ZADD`, `ZRANGEBYSCORE`, `ZSCORE` | `ingest.Builder`, `search.Page` |
| Jaccard index | How alike two documents are | computed from `SMEMBERS` | `similarity.Jaccard` |
| Set difference, again | Re-ingestion: what to add and remove for a changed document | `SADD` / `SREM` | `ingest.Builder` |

The command names are the whole point: the Redis adapter (`internal/setstore/redis`) is a thin layer because each port method is one command.

## One query, step by step

```sh
lattice search --entity item \
  --facet kind=kind-1,kind-2 --facet tier=pro \
  --not region=region-03 \
  --range year=2018..2024
```

1. `kind=kind-1,kind-2` becomes the union `item:kind:kind-1 ∪ item:kind:kind-2`, stored in a temporary key.
2. `tier=pro` is already a set: `item:tier:pro`.
3. The range is a slice of the sorted set `item:_range:year`; its IDs become a third set.
4. The positive sets are intersected: `kind-union ∩ tier:pro ∩ year-range`.
5. The excluded set is subtracted: `… ∖ item:region:region-03`.
6. Temporary keys are deleted.

OR happens inside a field and AND across fields because a document has one value per field. Within a field the sets are alternatives, so the union is the right operation; across fields they are independent conditions, so the intersection is.

## Facet counts

For a current result set `R`, the count for a value `v` of some field is `|R ∩ S(v)|`, where `S(v)` is the set for that value. Lattice computes that for every known value of the fields you ask about, in a handful of round trips, and drops zeros.

In lattice terms, `R ∩ S(v)` is what you get by adding one more facet to the filter, so the counts tell you the size of every set one step further down the lattice of filters. That is how a CLI agent see the shape of a result before narrowing it. The lattice itself is never stored; it has a combinatorial number of points, and each step is computed on demand.

## Similarity

A document's *memberships* are the sets it belongs to: `role:admin`, `tags:go`, `tags:redis`, and so on. Two documents are as similar as their membership sets overlap:

```latex
J(a, b) = |M(a) ∩ M(b)| / |M(a) ∪ M(b)|
```

To find candidates cheaply, Lattice reads the sets the target belongs to and counts how often each other ID appears, so documents sharing nothing with the target are never scored. Pass `--paths` to compare only some fields.

## Re-ingestion is a set difference

Each document keeps its own membership set (`item:_memberships:<id>`). When a document changes, Lattice computes the new set of memberships and compares it with the stored one:

- `added = new ∖ old`, so `SADD` the ID into those sets
- `removed = old ∖ new`, so `SREM` the ID from those sets

A document that has not changed produces two empty differences, and nothing is written. That is why re-running ingestion on an unchanged folder is cheap and why updated files do not need a flush.

## Laws the tests check

The shared adapter suite (`internal/setstore/contract`) checks the identities the whole design leans on, against both the in-memory store and a live Redis:

- inclusion–exclusion: `|A ∪ B| = |A| + |B| − |A ∩ B|`
- the union splits cleanly: `A ∪ B = (A ∖ B) ∪ (A ∩ B) ∪ (B ∖ A)`, with the three parts disjoint
- Jaccard of `{1,2,3,4}` and `{3,4,5}` is `2/5`
- difference is not commutative; intersection and union are

## What is not set theory concepts (or at least not translatable)
- **Ordering and pagination.** Sets are unordered, so sorting looks scores up in a sorted set and orders them in Go. See [benchmarks](benchmarks.md) for what that costs and what the alternatives would do.
- **Text search.** At the moment, there is none; it is an inverted-index problem rather than a set problem.
- **Superset checks.** "Does A contain all of B" is `SDIFF B A` being empty. The port can express it (`Difference`), but no command exposes it yet.