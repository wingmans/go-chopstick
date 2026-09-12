# EDGAR Indexes and Filing Downloads

The EDGAR downloader stores quarterly index files under `data/indexes/` and
stores downloaded filing files under `data/filings/` by default.

## Index records

The master index is read as a stream. `IndexReader.Next` parses and returns one
`EdgarIndex` at a time, so a multi-gigabyte `master.tsv` does not need to fit in
memory. `IndexFilter` can restrict records by CIK and by one or more exact form
types.

The SEC fields are represented as follows:

| Field | Meaning |
| --- | --- |
| `CIK` | Central Index Key identifying the filer |
| `CompanyName` | Filer name associated with the filing |
| `FormType` | Submitted SEC form, such as `10-K` or `10-Q` |
| `DateFiled` | Filing date, parsed as `time.Time` |
| `FilingPath` | SEC-relative path to the raw submission text file |
| `IndexPath` | Derived SEC-relative path to the HTML filing index |

`IndexPath` is added by this repository. It is not one of the original master
index columns. The extractor derives it by changing the submission `.txt` path
to the corresponding `-index.html` path.

## Unique form types

`UniqueFormTypes` scans the master index through `IndexReader`, collects form
types in a set, and returns sorted unique values. The set is small even when
the source file is very large.

## Filing downloads

`DownloadIndexFiles` streams records from a master TSV and downloads the files
referenced by each `EdgarIndex`. It downloads `FilingPath` and `IndexPath`
sequentially.

The downloader is intentionally single-threaded. Before each network request
it checks whether the destination file already exists. Existing files are
skipped, so rerunning the downloader is idempotent and does not make redundant
SEC requests.

New files are written to a temporary file in the destination directory and are
renamed into place only after the response has been copied successfully. A
partial download therefore does not look like a completed file on the next
run.

Requests are paced with a two-second minimum request budget. This is far below
the SEC's 10 requests-per-second threshold and keeps the downloader
deliberately conservative. Local existence checks do not consume request
budget.

## Folder structure

The recommended layout is:

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
          0000950170-25-021128.txt
          0000950170-25-021128-index.html
```

The filings directory mirrors the SEC-relative paths exactly. CIK is part of
the path, but company names are not used because names can change. The
accession number in the filename identifies the submission, so a separate year
directory or year suffix is not required.

## CLI commands

The command-line layer is intentionally separate from the EDGAR logic so the
same operations can later be called by a service or an orchestrator.

```text
edgar download-index [options]
edgar download-filings [options]
```

`download-index` downloads quarterly indexes and stitches `master.tsv` by
default. `download-filings` reads the existing `master.tsv` and downloads the
referenced files independently. The default paths are
`./data/indexes/quarterly` for TSV indexes, `./data/cache/index-zips` for ZIP
files, `./data/indexes/master.tsv` for the master index, and `./data/filings`
for filings.

The default user-agent is `wingman paul@wingmen.io`. It can be overridden on
either subcommand with `--user-agent`.
