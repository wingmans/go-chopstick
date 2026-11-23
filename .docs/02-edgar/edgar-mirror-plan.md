# EDGAR Mirror V1 Implementation Plan

## Purpose

Build a small, in-process-capable EDGAR mirror that stores compressed raw SEC
submissions behind a transport-independent service. The first delivery is
isolated from the running event pipeline so its storage behavior, catalog
reconstruction, REST contract, and compression can be verified safely.

The mirror is a reconstructible cache. SEC remains the authoritative origin.
The mirror exists to make parser development and historical reprocessing fast
without repeated SEC requests or external bandwidth use.

The expected upper bound is a few terabytes. All content operations therefore
stream, all sizes use 64-bit integers, searches are paginated, and catalog
reconstruction handles one submission at a time.

## Controlled Integration Boundary

This patch must stop before pipeline integration.

V1 must not change:

- `make dev` or its managed roles;
- discovery, selection, download, or processing workers;
- News or filings-web behavior;
- the current uncompressed filing cache lifecycle; or
- existing environment defaults.

The mirror is exercised only through the explicit `edgar mirror` commands.
After V1 passes its acceptance gate, a separate, deliberately timed change can
construct one mirror service inside the managed monolith and replace the full
raw-file lifecycle atomically. There must be no intermediate state where the
download worker writes the mirror while processors still assume permanent
uncompressed files.

## V1 Architecture

```text
REST adapter
    |
Mirror service
    |-- compressed filesystem store
    |-- disposable SQLite catalog
    `-- paced SEC origin
```

Business behavior belongs in the mirror service. HTTP handlers only decode a
request, call one service method, map domain errors, and encode or stream the
response. A future gRPC adapter must be able to call the same service methods.

## Storage

The SEC-relative filing path is the stable logical object key:

```text
edgar/data/19617/0000019617-25-000270.txt
```

The filesystem backend stores:

```text
data/mirror/objects/
  edgar/data/19617/0000019617-25-000270.txt.zst
```

The backend validates keys, rejects traversal, compresses with zstd level 3,
enables zstd frame checksums, and publishes completed writes with an atomic
rename. Callers always read the original uncompressed byte stream.

The storage interface provides streaming `Stat`, `Put`, `Open`, and `Walk`
operations. It does not expose filesystem paths or compression suffixes.

## Catalog

SQLite contains only metadata that can be reconstructed from the raw object:

- logical object key;
- accession;
- CIK;
- form type;
- filing date and filing year;
- uncompressed and stored byte counts; and
- SHA-256 of the uncompressed submission.

The catalog supports paginated searches by CIK, form type, and filing year.
It is never required to calculate an object's physical path.

If the catalog database is absent at startup, the service automatically:

1. creates a temporary catalog;
2. walks the compressed object tree;
3. decompresses one object at a time;
4. reads its SEC envelope metadata;
5. calculates its size and SHA-256;
6. inserts catalog rows in bounded transactions;
7. validates the new database; and
8. atomically publishes it.

Reconstruction may be slow. It needs temporary space for only the largest
individual submission, not another copy of the complete archive.

## Service Semantics

`Ensure` returns an existing mirrored submission without contacting SEC. For a
confirmed miss, it retrieves the SEC archive path through one shared, paced
origin client, stores the object, and indexes it. Concurrent requests for one
key share one retrieval.

A storage failure is not a cache miss. An unavailable mirror must never cause
an accidental bulk SEC download.

## REST V1

The loopback-only REST API provides:

```text
GET  /health
POST /v1/submissions:ensure
GET  /v1/submissions/{accession}
GET  /v1/submissions/{accession}/content
GET  /v1/submissions?cik=&form=&filing_year=&limit=&cursor=
```

Search returns metadata. Content responses stream the uncompressed SEC
submission. Authentication and TLS are deferred until the service is allowed
to bind beyond loopback.

## CLI V1

```text
edgar mirror serve
edgar mirror import --source <current-cache>
edgar mirror stats
edgar mirror verify
edgar mirror repair
edgar mirror catalog rebuild
```

Import is idempotent and never modifies its source. Statistics provide the
first benchmark: object count, logical bytes, stored bytes, saved bytes, and
compression ratio. Verification walks storage independently from SQLite.
Repair reindexes valid objects and retrieves confirmed corrupt objects through
the SEC origin.

## Acceptance Gate

V1 is ready for a later integration change only when:

- all unit and integration tests pass;
- importing the current cache leaves the source unchanged;
- every imported object round-trips byte-for-byte;
- disk-space statistics are recorded;
- verification passes over the imported archive;
- deleting SQLite causes automatic, complete reconstruction;
- reconstructed metadata and row counts match;
- REST search and content streaming work against imported data;
- memory use remains bounded during import and reconstruction; and
- existing pipeline behavior and tests remain unchanged.

## Explicitly Deferred

- Managed-monolith and worker integration.
- Changes to `make dev`, Doctor, or Status.
- gRPC.
- S3 or an S3-compatible service.
- Multiple storage machines, placement, replication, or rebalancing.
- Per-object manifests.
- Presigned URLs.
- Stored extracted attachments.

If a second storage machine becomes an actual requirement, first reconsider a
maintained object store such as Garage. The filesystem interface preserves the
option to build another backend for learning without committing V1 to a
distributed-storage design.
