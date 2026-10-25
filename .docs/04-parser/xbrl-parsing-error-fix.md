# XBRL Parsing Error Log

This file is the running record for parsing and XBRL errors encountered in
local EDGAR filings. Add each new issue here after it has been understood and
fixed, preserving the behavior and reasoning that led to the change.

## Symptom

Some filings failed during processing with diagnostics like:

```text
xml: encoding "us-ascii" declared but Decoder.CharsetReader is nil
```

The errors referred to documents with names such as:

```text
msft-20260513_def.xml
msft-20260513_lab.xml
msft-20260513_pre.xml
```

## Cause

These files are XBRL taxonomy support documents:

- `_def.xml` contains definition linkbases.
- `_lab.xml` contains labels.
- `_pre.xml` contains presentation linkbases.

They are XML documents included in the filing inventory, but they are not the
filing's XBRL instance document. The parser was attempting to decode every XML
attachment as an instance. Some of these support files declare an encoding
that Go's XML decoder does not handle without a configured character-set
reader, causing extraction to fail before the useful filing data could be
read.

## Fix

Submission extraction now limits XBRL parsing to likely instance documents:

1. Documents explicitly identified as `EX-101.INS` are selected.
2. Documents whose filenames end in `_htm.xml` are selected as a conventional
   filing-instance fallback.
3. Other XML attachments, including `_def.xml`, `_lab.xml`, and `_pre.xml`,
   remain part of the submission inventory but are not parsed as filing
   instances.

This keeps the inventory complete while preventing taxonomy linkbases from
entering the instance parser.

## Diagnostic behavior

The SEC-declared document count is compared with the number of documents found
in the inventory. A mismatch is logged as a warning because incomplete or
unusual inventories can still contain a usable filing. It does not, by
itself, fail processing.

Actual extraction failures, such as an unreadable or invalid instance
document, continue to fail the filing and are recorded in the submission
diagnostics.

## Verification

The change is covered by submission-reader tests using `_htm.xml` instance
fixtures, and the full test suite passes with:

```bash
go test ./...
```

## Legacy EDGAR envelope and ASCII XBRL declarations

### Symptoms

Older Microsoft 10-K submissions produced either:

```text
expected <SEC-DOCUMENT>
```

or XBRL diagnostics such as:

```text
xml: encoding "us-ascii" declared but Decoder.CharsetReader is nil
```

### Cause

The 2010 submission was wrapped in a `PRIVACY-ENHANCED MESSAGE` envelope. The
actual SEC submission began after the PEM-style wrapper line. The 2011-2013
XBRL instance documents used valid ASCII content while explicitly declaring
`us-ascii` or `US-ASCII` in their XML declaration.

### Fix

The submission reader now accepts the privacy-enhanced wrapper and continues
to parse the enclosed SEC envelope. Wrapper lines are counted as part of the
source stream, so embedded document byte offsets remain unchanged.

The XBRL reader configures `CharsetReader` to pass through ASCII and UTF-8
content. Other unknown charsets still produce an extraction diagnostic instead
of being silently mis-decoded.

`LOGLEVEL` is also accepted as an alias for `LOG_LEVEL`, matching the spelling
used by existing command invocations.

### Verification

Regression fixtures cover the privacy-enhanced envelope and a `US-ASCII` XML
declaration. The full suite passes with `go test ./...`.

## Entry format for future errors

For each new issue, add a dated section containing:

- **Symptom:** the relevant log message, accession number, and document name.
- **Cause:** what the filing structure or parser assumption exposed.
- **Fix:** the parsing or diagnostic behavior that changed.
- **Verification:** the test, fixture, or command used to confirm the fix.

## Legacy malformed XML entities in 2010-2011 filings

### Symptoms

Nine filings in the broader US-GAAP validation set currently fail with
diagnostics such as:

```text
XML syntax error: invalid character entity &lt (no semicolon)
XML syntax error: invalid characters between closing tag and >
```

Examples include 2010 10-K filings for NVIDIA, Berkshire Hathaway,
Caterpillar, NextEra, and Costco, plus several 2011 filings.

### Cause

The affected SEC XBRL instance documents contain malformed XML. Entity
references such as `&lt` and `&gt` are missing their terminating semicolon,
and some closing tags contain truncated or invalid names. Go's XML decoder
correctly rejects these documents rather than silently changing their content.

These are source-format errors in legacy filings, not taxonomy misses,
network failures, or current pacing errors.

### Current behavior

For reviewed legacy patterns, the parser first runs the strict XML reader and
then retries an in-memory normalized copy only if strict parsing fails. The raw
SEC filing remains unchanged and its checksum remains the source lineage.
Successful retries record a legacy-normalization diagnostic on the parsed
filing. Other malformed XML still stores the filing with status `partial` and
records the original `xbrl_error`.

The workflow still treats unresolved `xbrl_error` diagnostics as processing
failures. A clean full SEC rerun is useful after the compatibility patch is
committed; doing it before then makes parser improvements hard to distinguish
from cache and download churn.

### Follow-up

Do not mutate the pristine raw filing. Compatibility code must keep using a
parser-only normalized copy of the affected XML, with the original bytes,
normalization rules, and diagnostics retained for lineage.

Tiny malformed fixtures now live in
`internal/edgar/testdata/malformed-xbrl/`. They preserve the failure patterns
without copying full SEC filings into the test suite. Tests assert both sides:
the strict XML reader rejects the malformed bytes, while submission parsing can
normalize reviewed patterns in memory and parse them successfully.
