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

They are XML documents included in the filing inventory, but they are not the filing's XBRL instance
document. The parser was attempting to decode every XML attachment as an instance. Some of these
support files declare an encoding that Go's XML decoder does not handle without a configured
character-set reader, causing extraction to fail before the useful filing data could be read.

## Fix

Submission extraction now limits XBRL parsing to likely instance documents:

1. Documents explicitly identified as `EX-101.INS` are selected.
2. Documents whose filenames end in `_htm.xml` are selected as a conventional filing-instance fallback.
3. Other XML attachments, including `_def.xml`, `_lab.xml`, and `_pre.xml`, remain part of the submission
   inventory but are not parsed as filing instances.

This keeps the inventory complete while preventing taxonomy linkbases from entering the instance parser.

## Diagnostic behavior

The SEC-declared document count is compared with the number of documents found in the inventory.
A mismatch is logged as a warning because incomplete or unusual inventories can still contain a usable filing. It does not, by itself, fail processing.

Actual extraction failures, such as an unreadable or invalid instance document, continue to fail the filing and are recorded in the submission diagnostics.

## Verification

The change is covered by submission-reader tests using `_htm.xml` instance fixtures, and the full test suite passes with:

```bash
go test ./...
```

## Entry format for future errors

For each new issue, add a dated section containing:

- **Symptom:** the relevant log message, accession number, and document name.
- **Cause:** what the filing structure or parser assumption exposed.
- **Fix:** the parsing or diagnostic behavior that changed.
- **Verification:** the test, fixture, or command used to confirm the fix.
