# Parsed JSON benchmark

This note records the first performance measurements for parsed filing JSON.
The experiment is deliberately separate from production persistence code in
`internal/jsonexp`.

## Scope

The benchmark compares:

- the current `encoding/json` implementation;
- `encoding/json` with an explicit buffered reader;
- the newer `encoding/json/v2` implementation;
- indented and compact JSON encoding;
- source fingerprint checks.

The package contains a small committed fixture for correctness checks. It also
uses a package-level path to a larger local parsed artifact when available:

```text
data/parsed/0000753308/0000753308-19-000118/filing.json
```

That artifact is approximately 81 MB. The benchmark skips the large-file
subtests when the local artifact is unavailable.

Run the benchmark with:

```sh
go test ./internal/jsonexp -run '^$' \
  -bench 'Benchmark(ParsedFilingDecode|ParsedFilingEncode|SourceFingerprint)$' \
  -benchmem -count=1
```

## Preliminary results

The following results were measured on an Apple M1 Pro running Go 1.27.1.
The large-file decode benchmark used the 81 MB parsed artifact.

### Decode

| Variant | Time | Allocated |
| --- | ---: | ---: |
| `encoding/json` | 404 ms | 441 MB |
| Explicit buffered reader | 380 ms | 441 MB |
| `encoding/json/v2` | 253 ms | 175 MB |

The small fixture showed the same direction. The explicit buffered reader was
slower for the small file and allocated an additional buffer. It does not
appear to be a useful general optimization.

`encoding/json/v2` decoded the large artifact successfully. The committed
fixture is decoded by both implementations and compared with
`reflect.DeepEqual`.

### Encode

| Variant | Time | Allocated |
| --- | ---: | ---: |
| Current indented JSON | 323 ms | 379 MB |
| Current compact JSON | 194 ms | 16.6 MB |
| `encoding/json/v2` compact | 108 ms | 2.4 MB |

The encoding measurements use the same large parsed filing. They indicate
that indentation has a substantial cost, and that `encoding/json/v2` may
improve both throughput and allocation volume.

### Fingerprint

The source fingerprint benchmark measured approximately:

```text
15 microseconds/op
1.1 KB/op
11 allocations/op
```

This is negligible compared with decoding or parsing a filing. The fingerprint
fast path therefore remains a good way to determine that no work is required.

## Pipeline observation

An end-to-end run over the current 19-company test set produced:

```text
Initial run: 1,194 seconds
Warm rerun:     25 seconds
Selected:     3,029 filings
Reused:       3,022 filings
Failed:           7 filings
```

The warm rerun took approximately 2.1% of the initial runtime. The seven
failures were deterministic `unclosed document text` parser errors and were
correctly retried rather than silently treated as reusable results.

This confirms that a warm run normally performs only:

1. filing selection from the index;
2. a raw-file metadata check;
3. a small fingerprint sidecar read;
4. an SQLite materialization lookup.

It should not load the large `filing.json`, parse XBRL, or rebuild the dividend
views when the materialized filing is complete.

## Initial conclusions

The benchmark supports the following conclusions:

- Explicit buffering is not the main optimization opportunity.
- `encoding/json/v2` is promising enough for a controlled production trial.
- Compact JSON is substantially cheaper to write than indented JSON.
- Fingerprint checks are cheap enough to keep on every warm-path filing.
- The production parser still uses the current `encoding/json` implementation.

The next step should be an experimental replacement of the parsed artifact
load/save implementation using `encoding/json/v2`, followed by the complete
golden, parser, dividend, and pipeline test suites.

The exact benchmark percentages should be confirmed with longer benchmark
runs or `benchstat`. The large artifact currently produces only a few
iterations per subtest, so the direction is more reliable than the exact
numbers.

## Scaling context

Using the initial 19-company run as a simple linear reference, a cold run for
500 companies would be approximately:

```text
1,194 seconds * (500 / 19) = 31,421 seconds
```

That is about 8.7 hours, excluding downloads. A warm validation run would be
roughly 11 minutes by the same calculation. These are planning estimates only;
filing counts, filing sizes, and 8-K activity vary significantly by company.
