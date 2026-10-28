# Ownership ingestion plan

## Goal

Build an all-EDGAR ownership dataset from SEC filings. Reuse the existing raw
filing cache and store normalized ownership data in `data/ownership.db`.

The product reports the latest ownership disclosed to the SEC. It does not
claim to represent actual real-time ownership.

Initial historical coverage starts on January 1, 2022. The same importer must
later extend coverage to 2020 and then to the common 2016 coverage horizon
without rebuilding the database.

## Agreed scope

- Process relevant filings for all EDGAR issuers, managers, and owners.
- Track manager-reported positions, not the constituents held inside an ETF.
- Preserve every imported disclosure and transaction needed for history.
- Derive current views without merging unlike filing semantics.
- Support structured Schedule 13D/G filings from December 18, 2024.
- Defer legacy, unstructured Schedule 13D/G parsing.
- Keep parsing code small and specific to each ownership form family.
- Cache authoritative raw submissions in the existing filing cache.
- Make imports restartable, idempotent, and independently auditable.

## Non-goals for the first version

- ETF portfolio constituents from Form N-PORT.
- Proxy-statement ownership tables from `DEF 14A`.
- Proposed sales from Form 144.
- Voting records from Form N-PX.
- A complete capitalization table for an issuer.
- Inferring transaction dates from changes between 13F snapshots.
- Parsing legacy `SC 13D` and `SC 13G` HTML or text filings.

## Form types

### Core forms

| Family | Form types | Meaning |
| --- | --- | --- |
| Institutions | `13F-HR`, `13F-HR/A` | Quarterly positions |
| 13F notices | `13F-NT`, `13F-NT/A` | Positions reported elsewhere |
| Insiders | `3`, `3/A` | Initial holdings |
| Insiders | `4`, `4/A` | Transactions and holdings after them |
| Insiders | `5`, `5/A` | Deferred or previously missed reports |
| Large owners | `SCHEDULE 13D`, `SCHEDULE 13D/A` | Large-owner state |
| Large owners | `SCHEDULE 13G`, `SCHEDULE 13G/A` | Large-owner state |

`13F-NT` and `13F-NT/A` do not contain position tables. They still need a
small parser so a missing holdings report is not interpreted as zero holdings.

### Deferred legacy forms

- `SC 13D`
- `SC 13D/A`
- `SC 13G`
- `SC 13G/A`

These forms remain recognized. The importer records them as unsupported until
a separate legacy parser is implemented. Raw submissions may still be cached.

## Important semantics

### Reported ownership

All user-facing values must be labelled as reported ownership. Filings can be
late, amended, incomplete, confidential, or limited by reporting thresholds.

### Snapshots and transactions

The form families are not interchangeable:

- A 13F row is a manager's position as of a calendar quarter end.
- A 13D/G row is a beneficial-ownership disclosure state.
- A Form 4 row is a transaction and usually includes ownership afterward.

Do not place all three into a generic additive ownership event table.

### Amendments

An amendment is a later filing whose form type ends in `/A`. Preserve both the
original filing and its amendments.

Amendments are not automatically additive:

- A 13F restatement replaces the earlier report for that manager and period.
- A 13F amendment adding holdings supplements the earlier report.
- A Form 3, 4, or 5 amendment corrects an earlier ownership report.
- A 13D/G amendment is an updated disclosure, not a share transaction.

An amendment does not overwrite the normalized rows from another accession.
Family-specific effective views apply amendment rules when selecting current
or historical values. Reprocessing the same accession with a newer parser may
replace that accession's rows transactionally.

### Ownership buckets

Do not silently sum these categories:

- direct and indirect ownership;
- non-derivative and derivative securities;
- different security classes;
- positions held through different indirect ownership arrangements;
- positions reported by multiple managers with shared discretion.

An aggregate may be added later only with a documented policy.

## Security identity

A CIK identifies an SEC entity. A ticker is an exchange symbol. A CUSIP
identifies a particular security or security class.

Form 13F normally identifies a position using issuer name, security class, and
CUSIP rather than issuer CIK. Forms 3, 4, and 5 identify the issuer by CIK.
Schedule 13D/G can help connect issuer CIKs and CUSIPs.

Store security identity separately from parties:

```text
security_id <-> CUSIP <-> issuer CIK <-> ticker
```

The mapping can be incomplete. Unmatched 13F positions remain queryable by
CUSIP and must not be discarded or joined using issuer-name guesses.

## Architecture

```text
master index or discovery event
             |
             v
    ownership form filter
             |
             v
  deduplicate by accession
             |
             v
 existing raw filing cache
             |
             v
     SEC envelope reader
             |
             v
 form-family XML parser
             |
             v
 data/ownership.db
             |
             v
 latest and historical views
```

### Reuse

Reuse the existing:

- application data directory resolution;
- master index reader and filters;
- SEC downloader, request pacing, and raw filing paths;
- SEC submission envelope metadata and document inventory;
- SQLite, embedded schema, and generated query conventions;
- logging and command error handling.

Do not route ownership forms through the financial-statement materializer.
Give ownership forms their own capability list, parser dispatch, store, and
command workflow.

### Raw cache

The complete SEC submission remains authoritative and is stored under the
existing `data/cache/filings` tree. `ownership.db` stores its path, hash, and
parser version. It does not store another raw filing blob.

### Filing identity

Use accession number as the filing identity. Structured Schedule 13D/G index
rows may list the same accession under an issuer and reporting owner. Select
all matching index rows but download and import each accession once.

Retain the observed index CIKs as provenance when useful. They are not the
canonical identity of the filing.

## Database outline

This is a logical model. Exact columns should be driven by parser fixtures.

### Common tables

`ownership_filings`

- accession number primary key;
- form type and form family;
- filing and report dates;
- raw cache path and source hash;
- parser and schema versions;
- amendment flag and available amendment metadata;
- import status, diagnostic, and timestamps.

`ownership_parties`

- stable internal party ID;
- CIK when supplied;
- reported name;
- party role and type when supplied.

`ownership_filing_parties`

- filing;
- party;
- role, such as issuer, manager, or reporting owner;
- reporting-owner relationship attributes.

`ownership_securities`

- stable internal security ID;
- issuer party when known;
- reported title and class;
- derivative or non-derivative kind.

`ownership_security_identifiers`

- security;
- identifier type, such as CUSIP or ticker;
- identifier value;
- source filing and confidence.

### Family tables

`institutional_positions`

- manager, report period, issuer name, class, and CUSIP;
- reported value, shares or principal amount, and share/principal kind;
- put/call indicator, investment discretion, other managers, and voting power;
- amendment disposition needed to build the effective quarterly report.

`institutional_notices`

- notice manager and report period;
- managers that report the positions;
- original or amended notice metadata.

`beneficial_ownership_reports`

- issuer, reporting group or owner, security, and CUSIP;
- event or state date;
- shares and percentage;
- sole and shared voting and dispositive powers;
- schedule kind and amendment status.

`insider_holdings`

- issuer, reporting owner, and security;
- direct or indirect ownership;
- nature of indirect ownership;
- amount and date;
- derivative details where applicable;
- source row and filing provenance.

`insider_transactions`

- issuer, reporting owner, and security;
- transaction and execution dates;
- transaction code and acquired/disposed indicator;
- amount, price, and ownership following the transaction;
- direct or indirect ownership;
- derivative details where applicable;
- source row and filing provenance.

Derivative-only fields may justify separate derivative tables if the parser
otherwise develops many ambiguous nullable columns. Prefer clarity over a
prematurely unified schema.

### Coverage table

`ownership_coverage`

- form family and time range;
- parser version;
- expected, processed, failed, and unsupported counts;
- pending, running, partial, or complete status;
- run start and completion timestamps.

Coverage describes processed filings, not guaranteed knowledge of every
position that existed on the coverage start date.

### Numeric storage

Do not use floating-point values for shares, prices, percentages, or monetary
amounts. Preserve source decimal values as validated decimal strings or scaled
integers where a fixed scale is defined.

## Import workflow

### Candidate selection

Stream the master index. Select recognized ownership form types for all CIKs
and requested dates. Do not load a year or form family into memory.

Derive the accession from the filing path, deduplicate candidates by
accession, and record enough index identity to resume after interruption.

### Processing one filing

For each unique candidate:

1. Check the ownership filing status and parser version.
2. Reuse or download the complete raw submission.
3. Read submission metadata and document inventory.
4. Select the expected family document by its document type.
5. Parse family-specific XML into a small typed result.
6. Validate filing identity and required fields.
7. Replace that accession's normalized rows in one SQLite transaction.
8. Record success, unsupported format, or a classified failure.

Reprocessing an accession must produce the same database state. A parser
version change may replace normalized rows for that accession, but it must not
delete the raw filing or other accessions.

### Error handling

A malformed filing must not stop an all-EDGAR run. Record its status and a
bounded diagnostic, continue the batch, and make it eligible for completion
or repair runs.

Cancellation and database errors stop the run cleanly. SEC request pacing
continues to use the existing downloader rules.

## Parser requirements

### Forms 3, 4, and 5

Parse the ownership XML primary document. Preserve:

- issuer and all reporting owners;
- owner-to-issuer relationship flags;
- non-derivative holdings and transactions;
- derivative holdings and transactions;
- transaction codes, dates, amounts, prices, and acquired/disposed flags;
- ownership following a transaction;
- direct or indirect ownership and its explanation;
- footnote identifiers and text needed to interpret values;
- amendment metadata and signatures.

Do not reconstruct decades of holdings when the filing reports ownership
following a transaction. Expose the latest reported holding per distinct
ownership bucket.

### Form 13F

Parse the cover, summary, and information table. Preserve:

- filing manager and report period;
- holdings, combination, or notice report kind;
- other included and reporting managers;
- amendment number and restatement or added-holdings kind;
- issuer name, class, CUSIP, value, amount, amount kind, put/call, discretion,
  other managers, and voting authority.

Treat a position as a quarterly snapshot. Changes between quarters are
inferred changes only, not dated purchases or sales.

### Structured Schedule 13D/G

Parse only `SCHEDULE 13D`, `SCHEDULE 13D/A`, `SCHEDULE 13G`, and
`SCHEDULE 13G/A` XML in the first version. Preserve:

- issuer, CUSIP, and security class;
- all reporting persons and group relationships;
- event date;
- shares and ownership percentage;
- sole and shared voting and dispositive powers;
- schedule type, filer category, and amendment metadata;
- structured narrative items needed for later display.

Treat an amendment as a later disclosure state. Do not add its shares to the
previous disclosure.

## Derived views

The first query layer should provide:

- effective 13F positions for a manager and quarter;
- latest reported institutional positions for a CUSIP;
- latest reported 13D/G state per owner, issuer, and security;
- insider transaction history;
- latest reported insider holding per ownership bucket;
- filing provenance for every returned row;
- coverage and unsupported-format warnings.

Current views must never hide the source date. Return both the report or
transaction date and filing date.

## Backfill strategy

### Stage 1: development coverage

Import January 1, 2022 through the present.

This is the first performance and correctness target. It is large enough to
exercise restarts, amendments, duplicates, and schema variations while using
the indexes already available locally.

Structured Schedule 13D/G coverage begins on December 18, 2024. Earlier
legacy forms are counted and reported as unsupported.

### Stage 2: initial expansion

Import January 1, 2020 through December 31, 2021 into the same database.

No schema reset or special migration should be required. Re-run completion
for the combined 2020-to-present interval afterward.

### Stage 3: common coverage horizon

Import January 1, 2016 through December 31, 2019 and run completion for the
full 2016-to-present interval.

Call 2016 the coverage horizon, not a complete ownership baseline. A position
opened before 2016 might not have a later filing in the supported forms.

### Processing order

Process recent periods before older periods so useful current data becomes
available early. Database results must not depend on import order.

Within a completion range, process deterministic accession order to make
progress and failures reproducible.

## Completion run

A completion run reconciles the selected master-index interval with
`ownership.db`:

1. Enumerate every expected recognized accession.
2. Deduplicate repeated index entries.
3. Skip successful accessions at the current parser version.
4. Import missing accessions.
5. Retry failed accessions.
6. Reprocess accessions produced by an older parser version.
7. Count recognized but deferred legacy formats as unsupported.
8. Verify normalized rows and source provenance for successful filings.
9. Store counts and completion status for each family and interval.

A range is complete only when every expected accession has a terminal outcome
at the current parser version. `Unsupported` is a visible terminal outcome,
not a successful parse.

The status command should be able to report results such as:

```text
13F          complete from 2022-01-01
Forms 3/4/5  complete from 2022-01-01
13D/G XML    complete from 2024-12-18
13D/G legacy unsupported before 2024-12-18
```

## Continuous ingestion

After the historical importer is reliable, add an ownership consumer for new
EDGAR discovery events.

The ownership consumer:

- uses the same ownership form registry;
- selects all CIKs rather than `EDGAR_SET` members;
- deduplicates by accession;
- reuses the same cache, parser, and transactional store path;
- acknowledges an event only after a durable terminal outcome;
- does not change financial-statement selection behavior.

Periodic completion runs remain necessary to catch missed events, parser
upgrades, and temporary failures.

## Implementation phases

### Phase 1: foundation

- Add the ownership form registry and family classification.
- Add `ownershipstore` with an embedded, versioned SQLite schema.
- Add filing, party, security, provenance, and coverage tables.
- Add a streaming candidate iterator with accession deduplication.
- Add a command that defaults to `data/ownership.db` and the existing cache.
- Prove idempotent import and restart behavior with synthetic fixtures.

Acceptance criteria:

- Duplicate index rows cause one filing import.
- Re-running a successful range produces no duplicate normalized rows.
- A parser-version change makes only stale accessions eligible.
- The financial-statement form registry and pipeline are unchanged.

### Phase 2: Forms 3, 4, and 5

- Add official and representative XML fixtures for all six form types.
- Implement the small Section 16 XML parser.
- Persist holdings, transactions, owners, securities, and footnotes.
- Add latest-reported holding and transaction-history queries.
- Test amendments, multiple owners, derivatives, and indirect ownership.

Acceptance criteria:

- Every stored value has accession and source-row provenance.
- Direct, indirect, derivative, and non-derivative values remain distinct.
- Amendments are retained without double-counting corrected rows.

### Phase 3: Form 13F

- Add fixtures for holdings, combination, notice, and amendment reports.
- Implement cover, summary, information-table, and notice parsing.
- Persist positions and manager relationships.
- Implement restatement and added-holdings effective views.
- Add CUSIP-based latest-position queries.

Acceptance criteria:

- `13F-NT` never produces a zero-position conclusion.
- Restatements replace and added-holdings amendments supplement correctly.
- Position changes are not exposed as precisely dated transactions.

### Phase 4: structured Schedule 13D/G

- Add current structured XML fixtures for all four form types.
- Implement reporting-person, issuer, security, and ownership-state parsing.
- Preserve multiple reporting persons and group relationships.
- Add latest-state queries and CUSIP-to-issuer identity evidence.
- Record legacy `SC` forms as unsupported.

Acceptance criteria:

- Duplicate issuer and owner index rows import one accession.
- Amendments update the effective disclosure rather than add shares.
- Queries expose the December 18, 2024 structured-coverage boundary.

### Phase 5: completion and operations

- Add completion reconciliation and coverage summaries.
- Add bounded retries and classified failure reporting.
- Add database and parser-version status output.
- Run and measure the 2022-to-present backfill.
- Add indexes only when measurements show a query or import bottleneck.

Acceptance criteria:

- An interrupted run resumes without manual cleanup.
- Expected, processed, failed, and unsupported totals reconcile.
- A failed accession can be repaired independently.
- SQLite remains a single-writer streaming workload with bounded memory.

### Phase 6: continuous ingestion and earlier history

- Connect ownership processing to new discovery events.
- Retain periodic completion reconciliation.
- Extend coverage to 2020 and then to 2016.
- Reassess legacy Schedule 13D/G parsing as a separate project.

## Test strategy

Use small checked-in SEC-derived fixtures that cover structure without keeping
unnecessarily large submissions.

Required test layers:

- parser unit tests for each form and amendment variant;
- store tests for idempotency, replacement, and effective views;
- workflow tests using duplicate and malformed index rows;
- golden tests for representative real filings;
- completion tests with missing, failed, unsupported, and stale outcomes;
- cancellation and restart tests;
- a measured local smoke run before each larger historical interval.

Do not suppress lint warnings with `//nolint` while implementing this plan
without explicit permission.

## Main risks and safeguards

### Scale

All-EDGAR Section 16 history contains millions of filings. Stream candidates,
use one SQLite writer, keep transactions filing-sized, and avoid loading a
year into memory.

### Identifier mismatch

CIK and CUSIP identify different things. Preserve unresolved securities and
build mappings from explicit evidence rather than issuer-name similarity.

### Misleading current values

Always return the source and as-of date. Keep ownership buckets separate and
show coverage warnings.

### Amendment double counting

Keep immutable source rows and put family-specific amendment behavior in
effective views with dedicated tests.

### Format evolution

Key successful imports by parser version. Preserve the raw filing so a parser
upgrade can rebuild normalized rows without another SEC request.

### Legacy completeness

Count deferred `SC 13D/G` filings explicitly. Never describe a range that
contains unsupported legacy filings as fully parsed.
