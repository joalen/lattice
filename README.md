# Lattice

A personal project exploring how **set theory** maps onto **Redis's native set data structures**, built as a way to learn Go properly, and to solve a real problem I kept running into: making a large folder of irregular, deeply-nested JSON queryable by an agent (or a script) without loading everything into context.

Named for the mathematical **subset lattice**, the structure formed by all possible subsets of a set under union/intersection, which is exactly what this system navigates when filtering and faceting.

---

## Motivation

This started from an idea a solutions architect at CarMax gave me while I interned there: that set theory (unions, intersections, differences, supersets) maps almost 1:1 onto Redis's set commands (`SUNION`, `SINTER`, `SDIFF`), and that building a small search system around that mapping would be a solid way to learn backend fundamentals properly instead of abstractly.

It turned into something more useful once I connected it to a real pain point: I've wanted a **degree-evaluator tool** for a while, and the actual data behind something like that, degree plan requirement trees becomes nasty in a specific way. It's not flat, and it's not even consistently shaped: section names, nesting depth, and where courses live in the tree all vary per plan. Most "ingest a folder of JSON" tooling quietly assumes stable field names across documents. That assumption breaks here on purpose, and figuring out how to handle that turned out to be the more interesting problem than the original search-system idea.

So Lattice ended up being two things layered on top of each other:
1. A **set-theory-driven search/filter engine**, backed by Redis, for a structured dataset.
2. A **generic, schema-agnostic ingestion engine** that can point at *any* folder of JSON, regardless of how irregular or dynamically-keyed it is, and still index it usefully.

(Note: the original concrete dataset was a real folder of many degree-plan JSON files. That data isn't used directly in this repo or its test fixtures, since the schema itself is private! Everything here is built and tested against public, unrelated datasets instead, see [Testing philosophy](#testing-philosophy).)

---

## The problem, more precisely

Two separate but related problems:

**1. Filtering/faceting a structured dataset efficiently.**
Given a dataset with categorical facets, numeric ranges, and multi-membership fields, how do you support fast filtered search, faceted counts, and similarity scoring, without standing up a full query planner or vector search stack? Redis's native `SET`/`ZSET`/`SINTER`/`SUNION` primitives turn out to solve most of this directly, cheaply, and legibly.

**2. Making irregular, dynamically-shaped JSON queryable without a hand-written schema.**
Most JSON schema inference tooling (jsoninf, ClickHouse's inference, the various "JSON → JSON Schema" generators) infers structure **per exact key path** across a corpus, it works well when field names are stable, and it silently breaks when they aren't. Real-world nested data (say a transaction export could have the following payload below) breaks that assumption on purpose. 

```json 
{
  "2026": {
    "Q1": {
      "Jan": {
        "001": {
          "Coffee": 4.50,
          "Groceries": 82.13
        },
        "002": {
          "Gas": 45.00
        }
      },
      "Q2": {
        "Apr": {
          "001": {
            "Rent": 1800
          }
        }
      }
    }
  }
}
```

Why this might break is because the keys are heterogeneous, where you have "2026" a year, "Q1" a quarter, "Jan" a month, and etc, to which you can't clearly generate a proper schema for. The academic literature on this (see [Related work](#related-work)) treats heterogeneous JSON schema discovery as closer to inferring a **tree regular grammar** than a flat relational schema, which is the right framing here, and the one this project's ingestion engine is built around.

## Related Works
`ingest.Signature`'s core choice: fingerprint by shape, discard key names wasn't arrived at blind. It's a direct response to specific, named limitations in the existing JSON schema inference literature:
- **Baazizi, Mohamed-Amine, et al., "Schema Inference for Massive JSON Datasets" (*Extending Database Technology (EDBT)* 2017), extended as "Parametric Schema Inference for Massive JSON Datasets" (VLDB Journal, 2019).** A distributed, MapReduce/Spark-based approach that infers a schema per exact field path across a corpus, with a dedicated type language for expressing unions and optional fields. This is the representative "path-based" strategy that most schema inference tooling follows. It scales well and handles union/optional types cleanly, but a path is only meaningful if the same key shows up at the same position across documents. That assumption is exactly what the above schema example violates by design.
- **Klettke, Störl & Scherzinger, "Schema Extraction and Structural Outlier Detection for JSON-based NoSQL Data Stores" (DBIS 2015).** Introduced a structure identification graph for hierarchical JSON relationships and framed irregular documents as *structural outliers* to be detected against an inferred baseline, rather than noise to be normalized away. That reframing, "which structures recur, and which are rare/anomalous", is closer to how `ingest.Discover`'s frequency tally is meant to be read than a traditional required/optional schema is.
- **Cánovas Izquierdo & Cabot, "Discovering Implicit Schemas in JSON Data" (ICWE 2013).** Proposed an iterative schema-merging technique: infer a schema from one document, then progressively fold in schemas from subsequent documents, refining unions and optionality incrementally rather than requiring a full-corpus pass upfront. That incremental-merge idea is what I had in mind for a goal on creating an idempotent re-ingestion system (diff and update indexes as source files change, rather than a full flush) is aiming for. Rather than full path-based schemas, we just apply to structural signatures.
- **Veinhardt Latták & Koupil, "A Comparative Analysis of JSON Schema Inference Algorithms" (ENASE 2022).** An independent, author-unaffiliated comparison of five approaches (including the three above, plus Sevilla et al. 2015 and Frozza et al. 2018), evaluated on both feature support and measured runtime across corpora from 1k to 128k documents. Its most load-bearing finding for this project: as of that survey, none of the five studied approaches can correctly infer **maps/dictionaries** as a distinct type, because a map's key is part of the actual *data*, not schema metadata, and every approach studied treats keys as structural information. That's an explicit, citable statement of exactly the problem this project's original dataset ran into: a requirement tree where section titles live in key position *are* the data, not the schema, which breaks every path-based approach in the survey by design rather than by implementation gap.

---

## Architecture: hexagonal (ports & adapters)

The core rule: **business logic doesn't know Redis exists.** Domain types and query/ingestion logic depend only on small interfaces ("ports"); Redis, HTTP, and anything else are pluggable "adapters" that implement those interfaces. This buys us two concrete things:
- **Testability**: an in-memory fake adapter lets the domain logic be developed and tested with zero real Redis running.
- **Swappability**: the storage backend can change (Redis → Valkey, or even a different tool entirely) without touching `search`, `facets`, `similarity`, or `ingest`. (Valkey (another in memory database) specifically is a wire-compatible fork of Redis 7.2.4; same RESP protocol, same command set, so that swap would need almost no code change at all. Now, if we considered an in-memory store without native set operations, like Memcached, yeah it can technically satisfy the same Go interface but would be a poor fit in practice as the interface guarantees pluggability, not that the backend is well-suited to the job.)

```sh
lattice/
├── go.mod
├── cmd/lattice/             # composition root: wires the Redis adapter into the CLI
├── internal/
│   ├── cli/                 # commands: discover, propose, ingest, search, get, similar
│   ├── domain/              # core types
│   ├── ports/               # SetStore interface and Op
│   ├── setstore/
│   │   ├── contract/        # behavior suite every adapter must pass
│   │   ├── mem/             # in-memory adapter
│   │   └── redis/           # Redis/Valkey adapter
│   ├── keyspace/            # the one place index keys are spelled
│   ├── ingest/              # flatten, discovery, mapping, idempotent index builder
│   ├── search/              # filters, ranges, NOT, sort, pagination
│   ├── facets/              # facet counts
│   ├── similarity/          # Jaccard similarity
│   ├── seed/                # synthetic data generator
│   └── fixture/             # small shared test dataset
├── bench/                   # benchmark harness (go run ./bench)
└── docs/
```

---

### Testing philosophy

Everything above has been exercised against **public datasets unrelated to the original degree-plan use case**, on purpose:
- [JSONPlaceholder](https://jsonplaceholder.typicode.com)'s `/users` endpoint: [confirmed] `Discover` finds the repeating user/address/geo shape with no hints.
- The [Go release manifest](https://go.dev/dl/?mode=json) (`go.dev/dl`): [confirmed] two-tier structural discovery (files nested inside releases) on real, stable-schema data, and used to exercise the `mem.Store` adapter end-to-end (filtering release files by OS/arch/kind).

This is deliberate: if "domain-agnostic ingestion" only actually works on data shaped like say degree plans or another similar dataset, it isn't actually domain-agnostic. Testing against unrelated public data is what makes my claim checkable rather than just asserted.

---

## Setup

```bash
brew install go redis
brew services start redis     # Redis 7.0+ or Valkey 7.2+ (SINTERCARD)

git clone https://github.com/joalen/lattice
cd lattice
go build -o lattice ./cmd/lattice
go test -race ./...
LATTICE_REDIS_ADDR=localhost:6379 LATTICE_REQUIRE_REDIS=1 go test ./...   # also runs the Redis adapter tests
```

## Usage
```sh
lattice discover ./data # which shapes recur, ignoring key names
lattice propose --entity plan ./data > mapping.json # proposed field mapping; edit it, then:
lattice ingest --mapping mapping.json ./data # idempotent: re-run it as files change

lattice search --entity plan \
  --facet major=computer_science,mathematics --facet degree_type=bs \
  --not department=business --range total_credits=120..128 \
  --sort total_credits --limit 20 --facets department,degree_type
lattice get --entity plan 42
lattice similar --entity plan --limit 5 42
```

propose treats a string field as a facet only if it has at most 30 distinct values that repeat; pass --max-categorical N for fields like major.

Everything prints JSON. search returns IDs with a short summary (top-level scalar fields, long strings cut off) so a CLI agent can decide what to fetch; get returns the full document. Connection settings: --redis / LATTICE_REDIS_ADDR and --prefix / LATTICE_PREFIX.

## Benchmark and Performance
To test out the benchmark of this, run:
```sh
redis-server --port 6379 &
go run ./bench -out docs/benchmarks.md
```

This runs 300, 10K, 100K synthetic documents on Redis and the in-memory adapter (roughly 10 minutes), measuring p50/p95/p99 and Redis round trips for each operation plus some milestone goals of SINTERSTORE-vs-in-app and ZRANGE-vs-SORT comparisons. To get a faster run, try `-sizes 300,10000`