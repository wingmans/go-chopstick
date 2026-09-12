# EDGAR Data Decisions

This document records the storage and processing decisions for the EDGAR
downloader.

## Data layout

The local data directory is split by the role of each file:

```text
data/
  cache/
    index-zips/
      2025-QTR1.zip
      2025-QTR2.zip

  indexes/
    quarterly/
      2025-QTR1.tsv
      2025-QTR2.tsv
    master.tsv

  filings/
    edgar/
      data/
        1000045/
          0000950170-24-003550.txt
          0000950170-24-003550-index.html
```

### Cached index ZIP files

ZIP files downloaded from the SEC are kept under `data/cache/index-zips`.
They are untouched upstream downloads and can be retained for reproducibility
or removed later if disk usage becomes a concern.

The name `index-zips` is intentional. `archives` is too ambiguous because the
filing resources themselves are not archive files.

### Quarterly TSV files

Extracted quarterly TSV files are stored under `data/indexes/quarterly`.
They are not considered raw because extraction removes the SEC header lines and
adds the derived `IndexPath` field.

### Master TSV

The stitched `master.tsv` is stored under `data/indexes`, next to its source
quarterly indexes. It is derived data, but it is still an index/catalog rather
than application output. Keeping it there makes its relationship to the
quarterly files clear and avoids cluttering the `data` root.

## Filing paths

Downloaded filing files are stored under `data/filings` while preserving the
SEC-relative path:

```text
data/filings/edgar/data/<CIK>/<accession>.txt
data/filings/edgar/data/<CIK>/<accession>-index.html
```

The `edgar/data` portion is retained. This mirrors the SEC URL layout and makes
the mapping straightforward:

```text
data/filings/edgar/data/...
https://www.sec.gov/Archives/edgar/data/...
```

Company names are not used in local paths because they can change over time and
may contain characters unsuitable for filenames. CIK and accession paths are
stable identifiers.

## Compression and encoding

SEC responses may be gzip-compressed. Compression is not the same as character
encoding. The downloader must decompress gzip responses before writing `.txt`
and `.html` files to disk so they are readable by normal tools such as VS Code.

The downloader should let Go's default HTTP transport manage gzip negotiation,
or explicitly decompress responses based on `Content-Encoding`. It should not
blindly transcode all content to UTF-8 because SEC submissions can contain
historical or mixed character encodings. The downloaded bytes should otherwise
be preserved.

Existing files created by an older compressed implementation need special
handling. The downloader must not blindly skip a file merely because it exists
if it is known to be gzip-compressed. Any conversion should use a temporary
file and an atomic rename.

## Index processing

Index processing is streaming. `IndexReader` returns one `EdgarIndex` at a time,
which keeps memory usage constant for large master files.

`UniqueFormTypes` scans through the reader and stores only the distinct form
types. Filtering by CIK and form type happens while reading, before records are
passed to the downloader.

## Download behavior

The download process is intentionally single-threaded.

For each referenced filing path:

1. Resolve the SEC-relative path to its local path under `data/filings`.
2. Check whether a valid local file already exists.
3. Skip the network request when the file is already present.
4. Wait for the request budget before making a new SEC request.
5. Download into a temporary file in the destination directory.
6. Rename the temporary file into place only after the download succeeds.

This makes the process idempotent and restartable. Interrupted or failed
downloads do not appear as completed files on the next run, and reruns do not
redownload completed files.

Requests are paced conservatively, with a two-second minimum interval between
network request starts. This is well below the SEC's 10 requests-per-second
threshold. Local filesystem checks do not consume request budget.

Every request must use a descriptive SEC user-agent containing contact
information.

## CLI boundaries

The CLI is an adapter around the reusable EDGAR package. It is responsible for:

- parsing subcommands and flags;
- applying user-facing defaults;
- constructing an HTTP client;
- logging command progress; and
- delegating work to `internal/edgar`.

The EDGAR package owns index parsing, filtering, stitching, download behavior,
path handling, and rate limiting. This separation allows the same operations to
later be called by a service, scheduled job, or orchestration agent without
simulating command-line arguments.

The intended commands are:

```text
edgar index [options]
edgar filings [options]
```

The planned defaults are:

```text
index:
  quarterly indexes: ./data/indexes/quarterly
  ZIP files:         ./data/cache/index-zips
  master index:      ./data/indexes/master.tsv

filings:
  master index:      ./data/indexes/master.tsv
  filings:           ./data/filings
```

Stitching is enabled by default for `index`. Filing downloads remain a
separate command so index acquisition and filing acquisition can be run,
retried, and monitored independently.

## Atomic master creation

Stitching must write the new master index to a temporary file in the same
directory as the final `master.tsv`. After all quarterly files have been copied
successfully, the temporary file is renamed to `master.tsv`.

The final rename is atomic on the same filesystem. A failed stitch therefore
leaves the previous master index intact and never exposes a partial master file
to a reader or downloader.
