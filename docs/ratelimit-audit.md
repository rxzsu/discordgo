# Rate limiter audit

The audit covers bucket eviction, concurrent use, shared global/local
cooldowns, HTTP 429 handling, and allocation costs.

## Correctness

- Pruning formerly removed an expired bucket even when callers still held
  its address. A subsequent lookup created a different mutex for the same
  endpoint. Evicted buckets now have weak references so lookups reuse the
  same object while it remains live. Dead weak references are swept during
  pruning. A directly reused bucket is strongly cached again when its
  response establishes a new cooldown.
- Explicit removal retains locked buckets and active cooldowns. This
  intentionally replaces the old optimistic reset behavior.
- Waits honor the longer local/global deadline and recheck shared deadlines
  after waking. Global updates extend deadlines atomically.
- A 429 response retains its bucket lock until its body is decoded and
  shared cooldowns are updated. Successful responses still release the
  bucket before reading the body. A global 429 affects other endpoints
  even with automatic retries disabled.
- Retry durations preserve sub-millisecond precision. Negative, non-finite,
  and overflowing durations are rejected.

Regression tests include concurrent pruning, garbage collection, direct
reuse of an evicted bucket, global cooldown extension during a wait,
overlapping response limits, and a blocked 429 body. These checks do not
require Discord credentials.

## Benchmarks

Compare the code at commit `0f70bc8` with the fixes using the same
`ratelimit_bench_test.go`. The baseline benchmark file was copied to a
temporary checkout export; the worktree was preserved.

Command:

```sh
GOMAXPROCS=5 go test -run '^$' \
  -bench '^(BenchmarkGetBucketSerial|BenchmarkLimiterLockParallel|BenchmarkLimiterChurn)$' \
  -benchmem -benchtime=300ms -count=3 .
```

Observed on Linux amd64, Go 1.27.3, Intel Xeon Platinum 8573C. Values below
are medians of three short runs:

| Operation | Before, ns/op | After, ns/op | Before, B/op | After, B/op |
| --- | ---: | ---: | ---: | ---: |
| Cached lookup | 137.6 | 108.0 | 0 | 0 |
| Parallel lock/release, one endpoint | 844.7 | 515.2 | 0 | 0 |
| Parallel lock/release, 128 endpoints | 787.9 | 260.9 | 0 | 0 |
| Lock/release cycling through 16,384 endpoints | 2638 | 1557 | 134 | 21 |

Scheduling noise was substantial: cached lookup alone ranged from
78.32 to 212.2 ns/op after the change. These samples establish no reliable
latency speedup. Cached operations retain zero allocations; eviction/reuse
costs depend on garbage collection and outstanding references. Churn used
19–24 B/op after the change versus 133–134 B/op in these baseline runs.
Reported integer allocs/op for churn are rounded and do not imply that no
buckets or weak handles were allocated.

No live Discord load test was performed.
