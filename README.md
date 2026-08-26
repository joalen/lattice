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

Why this might break is because the keys are heterogeneous, where you have "2026" a year, "Q1" a quarter, "Jan" a month, and etc, to which you can't clearly generate a proper schema for. The academic literature on this (see [Related work](#related-work)) treats heterogeneous JSON schema discovery as closer to inferring a **tree regular grammar** than a flat relational schema — which is the right framing, and the one this project's ingestion engine is built around.


---

## Architecture: hexagonal (ports & adapters)

The core rule: **business logic doesn't know Redis exists.** Domain types and query/ingestion logic depend only on small interfaces ("ports"); Redis, HTTP, and anything else are pluggable "adapters" that implement those interfaces. This buys us two concrete things:
- **Testability** — an in-memory fake adapter lets the domain logic be developed and tested with zero real Redis running.
- **Swappability** — the storage backend can change (Redis → Valkey, or even a different tool entirely) without touching `search`, `facets`, `similarity`, or `ingest`. (Valkey (another in memory database) specifically is a wire-compatible fork of Redis 7.2.4; same RESP protocol, same command set, so that swap would need almost no code change at all. Now, if we considered an in-memory store without native set operations, like Memcached, yeah it can technically satisfy the same Go interface but would be a poor fit in practice as the interface guarantees pluggability, not that the backend is well-suited to the job.)

```sh
lattice/
├── go.mod
├── main.go                  # composition root — wires adapters into the domain
├── internal/
│   ├── domain/              # core types (Document, etc). Zero deps on Redis/HTTP.
│   ├── ports/               # interfaces the domain needs (SetStore, ...)
│   ├── setstore/
│   │   ├── mem/             # in-memory fake adapter
│   │   └── redis/           # real Redis/Valkey adapter
│   ├── search/              # filter DSL -> set ops
│   ├── facets/              # facet count computation 
│   ├── similarity/          # Jaccard similarity scoring
│   ├── ingest/              # generic JSON discovery + structural signatures
│   ├── api/                 # HTTP handlers
│   └── seed/                # test data generator
└── bench/                   # perf benchmarks
```

---

### Testing philosophy

Everything above has been exercised against **public datasets unrelated to the original degree-plan use case**, on purpose:
- [JSONPlaceholder](https://jsonplaceholder.typicode.com)'s `/users` endpoint — confirmed `Discover` finds the repeating user/address/geo shape with no hints.
- The [Go release manifest](https://go.dev/dl/?mode=json) (`go.dev/dl`) — confirmed two-tier structural discovery (files nested inside releases) on real, stable-schema data, and used to exercise the `mem.Store` adapter end-to-end (filtering release files by OS/arch/kind).

This is deliberate: if "domain-agnostic ingestion" only actually works on data shaped like say degree plans or another similar dataset, it isn't actually domain-agnostic. Testing against unrelated public data is what makes my claim checkable rather than just asserted.

---

## Setup

```bash
brew install go redis
brew services start redis

git clone https://github.com/joalen/lattice
cd lattice
go run main.go
```