# Local Submission Parser and XBRL Reader

Status: initial local reader and persistence implementation available in
`internal/edgar`. Financial interpretation and taxonomy mapping remain future work.

## Usage

```sh
go run ./cmd/edgar parse --file golden/sample_10-K.txt
go run ./cmd/edgar parse -f golden/sample_10-Q.txt -f golden/sample_8-K.txt
go run ./cmd/edgar parse -n -f golden/sample_10-K.txt
```

`-f, --file` is required and repeatable. `-n, --noop` runs parsing without writing
results; it defaults to false. Output goes to
`data/parsed/<CIK>/<accession>/filing.json`. No network access is performed.

Library entry points are `ParseSubmission`, `SaveParsedFiling`, and
`LoadParsedFiling`. `MatchesSource` checks parser/schema versions and the source
checksum for callers that want to reuse results; the command currently reparses
each explicitly requested file. Filings expose metadata getters and document and
instance collections. Each instance exposes `FactsByConcept`, `Context`, and
`Unit`, keeping references scoped to the document that defines them.

Header dates retain their `YYYYMMDD` representation; XBRL period strings retain
their XML-decoded values. Filers and item information are available as collections,
with the first filer CIK used for storage. The raw ordered header is also retained.

The current golden baselines are 130 documents / 1,869 facts for the 10-K,
106 documents / 1,580 facts for the 10-Q, and 10 documents / 28 facts for the 8-K.
The 10-Q's declared document-count discrepancy remains a diagnostic.

## Scope

Start with local SEC submission files and extract their structure and XBRL data.
The initial fixtures are the 10-K, 10-Q, and 8-K submissions in `golden/`.
Downloading, financial interpretation, and taxonomy harmonization are outside
this first step.

```text
Local .txt submission
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
`EX-101.INS` documents, then verifies the root namespace.

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
The command logs diagnostics but does not use them alone as a failing exit status.

`complete` describes extraction, not XBRL schema or taxonomy conformance. This is
not a full XBRL validator. Tuple interpretation, inline conversion, external schema
resolution, and non-UTF-8 transcoding are not implemented. Structured context and
footnote content is retained as an XML element/text tree; original bytes remain
available through the source file. XML nesting is limited to 256 elements.

Inventory earnings-release exhibits now. Extracting their HTML financial tables
belongs to a later adapter, not the initial XBRL reader.

## Persistence

Initially persist one versioned JSON document per filing:

```text
data/parsed/<CIK>/<accession>/filing.json
```

Include filing metadata, document inventory, XBRL facts, contexts, units,
references, diagnostics, and an explicit extraction status. Include the output
schema version, parser version, source path, and source-file checksum so a later
run can determine whether the parsed result is reusable or needs rebuilding.

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
