# Lattice benchmark results

Generated 2026-10-26 by `go run ./bench`.

- Go go1.27.2 on darwin/arm64, 12 CPUs
- Redis 8.10.1, same machine over loopback, so network latency is close to zero
- Synthetic catalog from `internal/seed` (seed 1); every query picks its parameters from real documents
- Up to 300 samples per operation (at least 30, time budget 5s), after 10 warm-up runs
- "matches" is the median number of documents the query matched; "round trips" counts network round trips to Redis (a pipeline or transaction is one)

## redis, 300 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 67.88 ms | 4420 | 903 | created 300, updated 0, unchanged 0 |
| re-ingest, nothing changed | 17.48 ms | 17161 | 601 | created 0, updated 0, unchanged 300 |
| re-ingest, 1% of documents changed | 17.21 ms | 17428 | 604 | created 0, updated 3, unchanged 297 |

Redis holds 1009 keys and uses 2.7 MB after ingestion.

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 20 µs | 36 µs | 47 µs | 1 | 300 |
| 1 facet, page of 20 | 24 | 29 µs | 56 µs | 75 µs | 1 | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 11 | 300 µs | 382 µs | 459 µs | 8 | 300 |
| OR within a facet AND another facet | 20 | 83 µs | 122 µs | 167 µs | 3 | 300 |
| NOT: one facet minus another | 11 | 78 µs | 131 µs | 162 µs | 3 | 300 |
| multi-valued field (tag) AND facet | 5 | 26 µs | 44 µs | 56 µs | 1 | 300 |
| broad filter, sorted, first page | 186 | 216 µs | 262 µs | 314 µs | 2 | 300 |
| broad filter, sorted, deep page (offset 15) | 186 | 214 µs | 253 µs | 336 µs | 2 | 300 |
| facet counts over a narrow filter | 2 | 193 µs | 276 µs | 339 µs | 5 | 300 |
| facet counts over a broad filter | 76 | 380 µs | 513 µs | 598 µs | 8 | 300 |
| similar(id), top 10 | 10 | 924 µs | 1.14 ms | 1.21 ms | 3 | 300 |

### Intersection: SINTERSTORE vs in-app

tier=free (186 ids) ∩ group=group-00 (99 ids) = 60 ids. The column "result" is the intersection size.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SINTERSTORE into a temp key, count stays server-side | 60 | 33 µs | 46 µs | 66 µs | 1 |
| SINTERCARD (count only) | 60 | 24 µs | 39 µs | 51 µs | 1 |
| SINTER (server computes, ids come back) | 60 | 32 µs | 51 µs | 62 µs | 1 |
| in-app: SMEMBERS both, intersect in Go | 60 | 48 µs | 67 µs | 90 µs | 1 |

### Pagination, 20 results at offset 0 of tier=free (186 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 62 µs | 77 µs | 93 µs | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 86 µs | 104 µs | 125 µs | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 218 µs | 261 µs | 342 µs | 2 |

### Pagination, 20 results at offset 93 of tier=free (186 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 63 µs | 83 µs | 97 µs | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 86 µs | 103 µs | 113 µs | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 219 µs | 250 µs | 316 µs | 2 |

Temporary keys left behind after the run: 0

## redis, 10000 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 1.20 s | 8335 | 30003 | created 10000, updated 0, unchanged 0 |
| re-ingest, nothing changed | 545.43 ms | 18334 | 20001 | created 0, updated 0, unchanged 10000 |
| re-ingest, 1% of documents changed | 556.02 ms | 17985 | 20101 | created 0, updated 100, unchanged 9900 |

Redis holds 20596 keys and uses 13.6 MB after ingestion.

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 22 µs | 38 µs | 57 µs | 1 | 300 |
| 1 facet, page of 20 | 866 | 158 µs | 554 µs | 570 µs | 1 | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 354 | 4.43 ms | 7.22 ms | 9.31 ms | 8 | 300 |
| OR within a facet AND another facet | 662 | 677 µs | 1.45 ms | 1.88 ms | 3 | 300 |
| NOT: one facet minus another | 520 | 556 µs | 1.43 ms | 1.55 ms | 3 | 300 |
| multi-valued field (tag) AND facet | 169 | 116 µs | 309 µs | 358 µs | 1 | 300 |
| broad filter, sorted, first page | 5976 | 11.42 ms | 19.95 ms | 22.49 ms | 7 | 300 |
| broad filter, sorted, deep page (offset 500) | 5976 | 12.22 ms | 19.47 ms | 22.23 ms | 7 | 300 |
| facet counts over a narrow filter | 27 | 531 µs | 1.04 ms | 1.58 ms | 5 | 300 |
| facet counts over a broad filter | 2529 | 6.26 ms | 9.67 ms | 10.63 ms | 8 | 300 |
| similar(id), top 10 | 10 | 51.65 ms | 64.01 ms | 69.51 ms | 12 | 97 |

### Intersection: SINTERSTORE vs in-app

tier=free (5976 ids) ∩ group=group-00 (3066 ids) = 1824 ids. The column "result" is the intersection size.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SINTERSTORE into a temp key, count stays server-side | 1824 | 484 µs | 848 µs | 1.01 ms | 1 |
| SINTERCARD (count only) | 1824 | 176 µs | 211 µs | 223 µs | 1 |
| SINTER (server computes, ids come back) | 1824 | 264 µs | 293 µs | 371 µs | 1 |
| in-app: SMEMBERS both, intersect in Go | 1824 | 865 µs | 1.06 ms | 1.16 ms | 1 |

### Pagination, 20 results at offset 0 of tier=free (5976 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 1.77 ms | 1.92 ms | 2.20 ms | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 2.07 ms | 2.19 ms | 2.42 ms | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 12.63 ms | 19.26 ms | 22.22 ms | 7 |

### Pagination, 20 results at offset 2988 of tier=free (5976 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 1.78 ms | 2.36 ms | 2.54 ms | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 2.06 ms | 2.23 ms | 2.48 ms | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 15.90 ms | 22.20 ms | 23.80 ms | 7 |

Temporary keys left behind after the run: 0

## redis, 100000 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 13.10 s | 7636 | 300003 | created 100000, updated 0, unchanged 0 |
| re-ingest, nothing changed | 5.63 s | 17777 | 200001 | created 0, updated 0, unchanged 100000 |
| re-ingest, 1% of documents changed | 5.67 s | 17630 | 201001 | created 0, updated 1000, unchanged 99000 |

Redis holds 200596 keys and uses 127.3 MB after ingestion.

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 24 µs | 246 µs | 898 µs | 1 | 300 |
| 1 facet, page of 20 | 8357 | 2.93 ms | 12.69 ms | 13.70 ms | 1 | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 3634 | 61.53 ms | 73.79 ms | 76.82 ms | 11 | 83 |
| OR within a facet AND another facet | 6049 | 5.98 ms | 16.76 ms | 20.25 ms | 3 | 300 |
| NOT: one facet minus another | 5313 | 4.65 ms | 17.50 ms | 20.07 ms | 3 | 300 |
| multi-valued field (tag) AND facet | 1766 | 1.88 ms | 7.14 ms | 8.24 ms | 1 | 300 |
| broad filter, sorted, first page | 59708 | 148.72 ms | 176.99 ms | 183.85 ms | 61 | 40 |
| broad filter, sorted, deep page (offset 5000) | 59708 | 146.59 ms | 175.42 ms | 179.32 ms | 61 | 42 |
| facet counts over a narrow filter | 304 | 2.91 ms | 6.74 ms | 7.25 ms | 5 | 300 |
| facet counts over a broad filter | 19729 | 61.26 ms | 79.87 ms | 85.81 ms | 8 | 83 |
| similar(id), top 10 | 10 | 438.25 ms | 484.00 ms | 490.10 ms | 94 | 30 |

### Intersection: SINTERSTORE vs in-app

tier=free (59708 ids) ∩ group=group-00 (31181 ids) = 18491 ids. The column "result" is the intersection size.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SINTERSTORE into a temp key, count stays server-side | 18491 | 4.66 ms | 6.07 ms | 6.62 ms | 1 |
| SINTERCARD (count only) | 18491 | 1.69 ms | 1.93 ms | 2.30 ms | 1 |
| SINTER (server computes, ids come back) | 18491 | 4.98 ms | 9.26 ms | 10.44 ms | 1 |
| in-app: SMEMBERS both, intersect in Go | 18491 | 11.83 ms | 14.59 ms | 15.31 ms | 1 |

### Pagination, 20 results at offset 0 of tier=free (59708 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 26.64 ms | 31.77 ms | 36.67 ms | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 33.00 ms | 35.63 ms | 37.80 ms | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 159.62 ms | 178.09 ms | 181.38 ms | 61 |

### Pagination, 20 results at offset 29854 of tier=free (59708 ids), ordered by score

SORT ALPHA orders by id, not by score, so it is the cheapest possible stable order rather than an equal-work comparison.

| approach | result | p50 | p95 | p99 | round trips |
|---|---|---|---|---|---|
| SORT ... ALPHA LIMIT  (approach 1) | 20 | 26.12 ms | 30.16 ms | 32.80 ms | 1 |
| ZINTERSTORE + ZRANGE  (approach 2) | 20 | 32.62 ms | 35.69 ms | 37.71 ms | 1 |
| current: fetch all ids, ZSCORE in batches, sort in Go | 20 | 156.23 ms | 173.95 ms | 182.51 ms | 61 |

Temporary keys left behind after the run: 0

## mem, 300 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 5.38 ms | 55731 | – | created 300, updated 0, unchanged 0 |
| re-ingest, nothing changed | 5.18 ms | 57869 | – | created 0, updated 0, unchanged 300 |
| re-ingest, 1% of documents changed | 5.52 ms | 54393 | – | created 0, updated 3, unchanged 297 |

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 250 ns | 1 µs | 11 µs | – | 300 |
| 1 facet, page of 20 | 24 | 5 µs | 18 µs | 22 µs | – | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 11 | 128 µs | 174 µs | 350 µs | – | 300 |
| OR within a facet AND another facet | 20 | 10 µs | 30 µs | 67 µs | – | 300 |
| NOT: one facet minus another | 11 | 9 µs | 23 µs | 42 µs | – | 300 |
| multi-valued field (tag) AND facet | 5 | 4 µs | 24 µs | 28 µs | – | 300 |
| broad filter, sorted, first page | 186 | 53 µs | 65 µs | 153 µs | – | 300 |
| broad filter, sorted, deep page (offset 15) | 186 | 54 µs | 63 µs | 110 µs | – | 300 |
| facet counts over a narrow filter | 2 | 16 µs | 33 µs | 92 µs | – | 300 |
| facet counts over a broad filter | 76 | 201 µs | 297 µs | 510 µs | – | 300 |
| similar(id), top 10 | 10 | 422 µs | 709 µs | 814 µs | – | 300 |

## mem, 10000 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 162.66 ms | 61478 | – | created 10000, updated 0, unchanged 0 |
| re-ingest, nothing changed | 107.08 ms | 93387 | – | created 0, updated 0, unchanged 10000 |
| re-ingest, 1% of documents changed | 107.51 ms | 93017 | – | created 0, updated 100, unchanged 9900 |

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 375 ns | 500 ns | 666 ns | – | 300 |
| 1 facet, page of 20 | 866 | 106 µs | 439 µs | 462 µs | – | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 354 | 3.97 ms | 4.98 ms | 5.84 ms | – | 300 |
| OR within a facet AND another facet | 662 | 273 µs | 850 µs | 1.53 ms | – | 300 |
| NOT: one facet minus another | 520 | 289 µs | 629 µs | 1.01 ms | – | 300 |
| multi-valued field (tag) AND facet | 169 | 130 µs | 822 µs | 860 µs | – | 300 |
| broad filter, sorted, first page | 5976 | 2.85 ms | 3.00 ms | 4.08 ms | – | 300 |
| broad filter, sorted, deep page (offset 500) | 5976 | 2.86 ms | 3.02 ms | 3.78 ms | – | 300 |
| facet counts over a narrow filter | 27 | 186 µs | 672 µs | 730 µs | – | 300 |
| facet counts over a broad filter | 2529 | 7.10 ms | 10.62 ms | 11.51 ms | – | 300 |
| similar(id), top 10 | 10 | 17.27 ms | 19.59 ms | 20.39 ms | – | 297 |

## mem, 100000 documents

| ingestion | time | docs/s | round trips | result |
|---|---|---|---|---|
| ingest, cold | 1.79 s | 55919 | – | created 100000, updated 0, unchanged 0 |
| re-ingest, nothing changed | 1.08 s | 92552 | – | created 0, updated 0, unchanged 100000 |
| re-ingest, 1% of documents changed | 1.07 s | 93850 | – | created 0, updated 1000, unchanged 99000 |

| operation | matches | p50 | p95 | p99 | round trips | samples |
|---|---|---|---|---|---|---|
| get(id) | 1 | 541 ns | 709 ns | 1 µs | – | 300 |
| 1 facet, page of 20 | 8357 | 1.49 ms | 6.12 ms | 6.80 ms | – | 300 |
| 3 facets (OR lists) + range, sorted, page of 20  [target] | 3670 | 60.08 ms | 78.83 ms | 86.53 ms | – | 84 |
| OR within a facet AND another facet | 6049 | 3.64 ms | 10.85 ms | 15.69 ms | – | 300 |
| NOT: one facet minus another | 5313 | 3.41 ms | 8.79 ms | 9.74 ms | – | 300 |
| multi-valued field (tag) AND facet | 1766 | 1.56 ms | 12.73 ms | 15.30 ms | – | 300 |
| broad filter, sorted, first page | 59708 | 41.59 ms | 49.11 ms | 59.39 ms | – | 150 |
| broad filter, sorted, deep page (offset 5000) | 59708 | 41.05 ms | 48.44 ms | 60.25 ms | – | 151 |
| facet counts over a narrow filter | 304 | 2.24 ms | 8.43 ms | 9.70 ms | – | 300 |
| facet counts over a broad filter | 20958 | 83.43 ms | 118.25 ms | 129.41 ms | – | 59 |
| similar(id), top 10 | 10 | 215.72 ms | 237.82 ms | 243.89 ms | – | 30 |
