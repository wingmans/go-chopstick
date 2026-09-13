# Local Submission Parser and XBRL Reader

Status: local parsing and persistence are integrated with the `filings` workflow.
The coordinator lives in `internal/filingworkflow`; `internal/edgar` contains
EDGAR parsing, XBRL, and persistence primitives.
Financial interpretation and taxonomy mapping remain future work.

## Usage

```sh
go run ./cmd/edgar filings -c 0000789019 -f 10-Q -y 2025
go run ./cmd/edgar filings -c 0000789019 -f 10-Q -y 2025 --reprocess
go run ./cmd/edgar filings -c 0000789019 -n
go run ./cmd/edgar parse -c 0000789019 -f 10-Q -y 2025
go run ./cmd/edgar parse --file golden/sample_10-K.txt
go run ./cmd/edgar parse --file golden/sample_10-K.txt --reprocess
go run ./cmd/edgar serve
```

`filings` is the normal download-and-process command. Existing CIK, form-type,
and filing-year filters select records from `data/indexes/master.tsv`; an empty
year selects all filing years. The source submission and any referenced HTML
index are downloaded only when missing. Processing then uses the local submission.

`parse` is also batch-oriented. With no `--file`, it reads the default
`data/indexes/master.tsv`, applies the same `--cik`, `--form-type`, and `--year`
filters, and processes submissions already present under `data/filings`. It never
downloads missing submissions. Missing local sources are skipped and logged, so
the command naturally processes all downloaded but unprocessed filings selected
by the master index. The default year is empty, meaning all years.

`--file <path>` is an optional explicit local-file escape hatch and may be repeated.
It is mutually exclusive with the master-index filters. It is intentionally
long-only: `-f, --form-type` must retain the same meaning on both `parse` and
`filings`. This is the one deliberate exception to the usual short/long alias
convention.

Both commands provide `-r, --reprocess`, default false. It bypasses reuse and
rebuilds parsed output; it does not force redownloading or refresh the HTML index.
No separate skip-processed flag is needed: reuse is the default.

Both commands provide `-n, --noop`, default false. This is now a planning operation:
it reads local headers and checks existing parsed results and source checksums,
but does not extract XBRL, download, normalize gzip files, or write output. Missing
submissions and legacy compressed sources are reported as needing preparation
before parsing. `parse` reports missing explicit `--file` inputs as errors instead
of downloading; master-index records whose local source is absent are skipped.
With `--noop --reprocess`, rebuilding is planned but never performed.

Workflow entry points in `internal/filingworkflow` are `ProcessFilings`,
`ProcessLocalFilings`, `ProcessLocalSubmissions`, and `ProcessSubmission`. The low-level
`internal/edgar` entry points `ParseSubmission`, `SaveParsedFiling`, and
`LoadParsedFiling` remain available separately. This dependency direction keeps
the orchestration layer from becoming part of the parser's domain API and keeps
the CLI itself thin. Filings expose metadata
getters and document and instance collections. Each instance exposes
`FactsByConcept`, `Context`, and `Unit`, keeping references document-local.

Header dates retain their `YYYYMMDD` representation; XBRL period strings retain
their XML-decoded values. Filers and item information are available as collections,
with the first filer CIK used for storage. The raw ordered header is also retained.

The current golden baselines are 130 documents / 1,869 facts for the 10-K,
106 documents / 1,580 facts for the 10-Q, and 10 documents / 28 facts for the 8-K.
The 10-Q's declared document-count discrepancy remains a diagnostic.

## Scope

Start with local SEC submission files and extract their structure and XBRL data.
The initial fixtures are the 10-K, 10-Q, and 8-K submissions in `golden/`.
Downloading belongs to the workflow coordinator in `internal/filingworkflow`,
not the parser. Financial
interpretation and taxonomy harmonization remain outside this step.

```text
Filtered master.tsv record -> Reuse/download local submission
  -> Shared processing coordinator <- Explicit local parse input
  -> Header identity and parsed-result lookup
  -> Reuse matching result OR:
  -> Submission reader
  -> Document inventory
  -> XBRL instance reader
  -> Persisted parsed filing
  -> Future taxonomy mapping
  -> Friendly filing getters
```

## Input and Validation

- Read from local disk only. Parsing must not fetch submissions, schemas, or
  taxonomy resources over the network.
- Use `.txt` as the submission-file convention. The temporary `.xml` extension
  used for viewing fixtures in an editor does not define another input format.
- Validate that the first nonblank line starts with `<SEC-DOCUMENT>`. This is a
  sanity check on the expected input, not general content-based format detection.
- Treat the outer submission as an SEC envelope, not as one XML document.
  Embedded documents can contain HTML, XML, or other attachment content.
- Keep the original submission unchanged as the source of truth.

The extension selects the expected input format; validation catches incorrect or
damaged input. Embedded XML still needs classification to distinguish an XBRL
instance from a schema, linkbase, or unrelated document.

## Shared Readers, Form-Specific Adapters

Use one submission reader and one namespace-aware XBRL reader across form types.
Separate envelope parsers for 10-K, 10-Q, and 8-K would duplicate structural logic
and make fixes harder to apply consistently.

Add form-specific adapters when behavior actually differs: selecting exhibits,
interpreting report sections, or extracting financial tables. Keep tests for
those adapters organized by form. New forms should reuse the shared readers
where their structure permits it.

## Submission Reader

Extract accession number, form type, filer information, filing date, report date,
and available item information. Preserve repeated and unknown header fields
alongside normalized metadata so later work does not require discarding or
guessing source information.

Use accession number as filing identity, with CIK as an organizational key.
Dates are metadata, not identifiers: multiple submissions can share a date.

Build an ordered inventory of embedded documents containing:

- Document type, sequence, filename, and description.
- An inferred role, kept separate from the original document type.
- Byte offsets or equivalent source references for locating the original payload.

Avoid copying every attachment into the parsed output or requiring every payload
to remain in memory. Support long lines and report truncated document boundaries
explicitly. Declared document-count discrepancies should produce diagnostics;
they should not automatically discard otherwise readable documents.

## XBRL Reader

Start with conventional extracted XBRL instance documents, commonly named
`*_htm.xml`, using Go's standard `encoding/xml` package. Filename and submission
metadata help locate candidates; the root element and namespace establish that
a candidate is an XBRL instance.

Do not assume `TYPE=XML` means the payload is XML: the golden submissions also use
that type for CSS and JavaScript. The reader selects `.xml` filenames or
`EX-101.INS` documents or conventional instance filenames ending in `_htm.xml`,
then verifies the root namespace. Taxonomy linkbases such as `_def.xml`, `_lab.xml`,
and `_pre.xml` remain in the document inventory but are not treated as instances.

Do not initially implement conversion of inline XBRL embedded in HTML. When an
extracted instance is present, use it rather than combining both representations
and double-counting facts. Report inline-only filings as an explicit unsupported
case for XBRL extraction, while retaining their submission inventory.

Preserve the following source-level information:

- Facts: concept, value, context reference, unit reference, decimals or precision,
  language, nil status, available IDs, and source document.
- Contexts: entity identifier and scheme, instant or duration, explicit and typed
  dimensions, and relevant structured context content.
- Units: measures, including numerator and denominator for divided units.
- Schema references and available footnote resources and relationships.

Represent concept and dimension names using namespace URI plus local name, not
just the prefix used by a particular document. Retain company-extension concepts
as well as standard taxonomy concepts.

Keep numeric values as exact strings initially, not `float64`. Preserve duplicate
fact occurrences rather than silently overwriting them. Missing facts, nil facts,
and zero values must remain distinguishable.

An extracted instance already represents values after inline transformations.
Do not apply inline scale, sign, or formatting transformations again. Do not
invent inline-specific attributes that are absent from the extracted source.

## Results and Diagnostics

Distinguish a successfully parsed submission with no XBRL from failed extraction.
An 8-K can remain useful through its metadata and exhibits even when there is no
financial XBRL instance to read.

Record diagnostics with source references where possible. Distinguish malformed
input, unsupported features, unresolved references, and metadata discrepancies.
Never present partially extracted XBRL as an unqualified complete result.

Current extraction statuses are `complete`, `no_xbrl`, `unsupported` (inline-only),
and `partial` (extraction errors, broken references, or unsupported instance
elements). Readable submissions with diagnostics can still be persisted; callers
must inspect the status. Envelope errors fail parsing and do not produce output.
Both commands continue after individual filing failures and return a nonzero exit
status when any filing failed. Download, envelope, persistence, malformed-XBRL,
and unresolved-reference errors count as failures. Unsupported features,
`no_xbrl`, and document-count warnings alone do not. Cancellation stops the batch;
an unreadable or malformed master index also stops traversal.

The final summary reports `selected`, `processed`, `reused`, `planned`, and
`failed`. Failure counts can overlap processed/reused counts: a persisted result
containing malformed-XBRL diagnostics is still a failed filing. Cached failures
continue to be reported without needlessly rerunning the same extraction.
Debug logs include the action, source location, filename, paths, and duration;
existing download logs distinguish cache from network.

`complete` describes extraction, not XBRL schema or taxonomy conformance. This is
not a full XBRL validator. Tuple interpretation, inline conversion, external schema
resolution, and non-UTF-8 transcoding are not implemented. Structured context and
footnote content is retained as an XML element/text tree; original bytes remain
available through the source file. XML nesting is limited to 256 elements.

Inventory earnings-release exhibits now. Extracting their HTML financial tables
belongs to a later adapter, not the initial XBRL reader.

## Persistence

Persist one versioned JSON document per filing:

```text
data/parsed/<CIK>/<accession>/filing.json
```

Downloads retain their SEC-relative paths and original filenames. Parsed results
use a ten-digit, zero-padded CIK and the hyphenated accession number. For example:

```text
data/filings/edgar/data/789019/0000950170-25-010491.txt
data/parsed/0000789019/0000950170-25-010491/filing.json
```

The JSON connects the two layouts explicitly:

```json
{
  "source_base": "filings",
  "source_path": "edgar/data/789019/0000950170-25-010491.txt"
}
```

Resolve that path relative to the configured filings directory (normally
`data/filings`). Explicit inputs outside that directory, such as golden files,
use `source_base: "absolute"` and an absolute `source_path`. This avoids relying
on the working directory when downstream code locates the original bytes.

Lookup reads the submission header, using the same first-filer ownership rule as
the full reader; if no FILER section exists, it uses the header CIK. All filers
remain in metadata. The CIK on a selected master-index row does not override this
ownership rule, and the accession prefix is never used to infer the filer CIK.
No source files are relocated to match the parsed layout.

Include filing metadata, document inventory, XBRL facts, contexts, units,
references, diagnostics, and an explicit extraction status. Include the output
schema version, parser version, source path, and source-file checksum so a later
run can determine whether the parsed result is reusable or needs rebuilding.

Reuse requires matching identity, source location, source SHA-256, parser version,
schema version, and a recognized extraction status. Changed bytes or locations,
missing/damaged JSON, and incompatible versions trigger rebuilding. Source-read
and filesystem permission failures are surfaced rather than silently ignored.
Matching `complete`, `no_xbrl`, `unsupported`, and `partial` results are reusable;
their status and diagnostics remain visible. Use `--reprocess` to retry unchanged
inputs explicitly, and bump the parser version when extraction behavior changes.

The output schema is now version 2 because source-path semantics include
`source_base`. Existing version-1 output is rebuilt lazily when selected. Old
noncanonical output directories are not deleted or migrated automatically.

Write through a temporary file followed by an atomic rename. Keep large embedded
payloads in the original submission and reference them from the inventory.

Keep this source-faithful representation separate from future harmonized data.
Taxonomy mappings and financial interpretations must be able to evolve without
changing or reparsing the underlying extracted facts unnecessarily.

## Filing API and Future Taxonomy Work

Provide a friendly filing API over the persisted model. Initial getters should
cover accession number, form type, dates, documents, and fact selection.

Defer convenience getters such as revenue or net income until their selection
rules are explicit. A concept name alone is insufficient: period, unit,
dimensions, duplicates, and quarterly versus year-to-date values can matter.

Prepare for taxonomy work by preserving namespaces, schema references, contexts,
and provenance. Do not invent a harmonized taxonomy in the first implementation.
Derived values should later retain references to their source facts and mapping
version.

## Testing and Implementation Order

1. Parse envelopes and document inventories for all three golden submissions.
2. Read extracted XBRL instances and verify representative facts, periods, units,
   dimensions, and reference resolution.
3. Persist and reload results, testing exact value preservation, version metadata,
   source checksums, and interrupted-write behavior.
4. Expose metadata and source-level fact getters over the persisted model.
5. Add form-specific interpretation and taxonomy mapping in subsequent work.

Add focused fixtures for alternate namespace prefixes, duplicate facts, nil and
zero values, long lines, truncated envelopes, no-XBRL submissions, and inline-only
submissions. The initial golden files are a starting point, not evidence of
compatibility with every issuer or filing generator; broaden coverage later.

Workflow tests cover first processing, reuse without rewriting, forced rebuilding
without network refresh, checksum/version invalidation, corrupt cached JSON,
multi-filer identity, relative source paths, filters, dry runs (including gzip),
cached diagnostic results, cancellation, and continuing batches after failures.

## Local dashboard

`serve` starts a standard-library `net/http` server over `data/parsed`. It reads
the persisted `filing.json` files locally and does not contact SEC EDGAR. Open
`http://127.0.0.1:8080/` after starting it. The dashboard lists parsed filings,
supports CIK filtering, and links each filing to its complete persisted JSON
representation for inspection.

## References

- Existing fixture notes: [10-K](10-K.md), [10-Q](10-Q.md), and [8-K](8-K.md).
- [XBRL 2.1 specification](https://www.xbrl.org/Specification/XBRL-2.1/REC-2003-12-31/XBRL-2.1-REC-2003-12-31%2Bcorrected-errata-2013-02-20.html)
  for instance structure and document-local context/unit references.
- [SEC webmaster FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions)
  for complete-submission header and document-format guidance.
- Design inspiration: [palafrank/edgar folder.go](https://github.com/palafrank/edgar/blob/master/folder.go)
  and [filing.go](https://github.com/palafrank/edgar/blob/master/filing.go).
  No runtime dependency on that library is planned.
- Consult SEC EDGAR documentation when format behavior is ambiguous; document
  assumptions and limitations rather than inferring universal rules from the
  three fixtures.
