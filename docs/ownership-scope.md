# Ownership Pipeline Scope

## Purpose

Keep automatic ownership processing small enough to validate and operate while
preserving the option to add more ownership families later.

The configured constituent set supplies issuer CIKs. Automatic discovery
currently selects these forms only when a master-index row for the accession
has a CIK in that set:

- Form 4 and 4/A;
- structured `SCHEDULE 13D` and `SCHEDULE 13D/A`; and
- structured `SCHEDULE 13G` and `SCHEDULE 13G/A`.

Forms 3/5 and 13F are deferred. Legacy `SC 13D/G` submissions are recognized
but remain unsupported because their primary documents are HTML rather than
the structured Schedule 13 XML handled by the parser.

## Why the Master Index Can Select Issuers

The SEC master index commonly contains one ownership accession under both the
reporting owner and the issuer. Discovery deduplicates by accession, so the
filing is selected when any row for that accession has a CIK in the configured
set.

This can select a filing in which a set member is the reporting owner of an
outside issuer. That is a harmless extra download. Missing an issuer filing is
the important failure mode.

## Validation Snapshot

The existing partial `ownership.db` was compared with the 2022 through 2026
master index before the clean rebuild. Parsed issuer relationships were the
oracle, and candidate selection used only master-index rows.

| Set | Family | Issuer filings | Selected | Missed | Recall |
|---|---|---:|---:|---:|---:|
| `us-gaap` | Form 4 | 249 | 249 | 0 | 100% |
| `sp500` | Form 4 | 5,145 | 5,145 | 0 | 100% |
| `sp500` | Schedule 13D | 4 | 4 | 0 | 100% |

The partial database contained no parsed Schedule 13G issuer relationships and
no Schedule 13D issuer relationships for `us-gaap`. Those cells could not be
validated. The clean rebuild must verify them after the current structured
Schedule 13G XML layout is processed successfully.

Candidate precision in the parsed sample was 92.2% for `us-gaap` Form 4 and
98.7% for `sp500` Form 4. Lower precision creates extra downloads but does not
lose issuer data.

## Clean-Rebuild Candidate Counts

The current structured-form policy produces these unique accession counts from
the retained master index:

| Set | Form 4/4A | Schedule 13D | Schedule 13G | Total |
|---|---:|---:|---:|---:|
| `us-gaap` | 8,402 | 86 | 523 | 9,011 |
| `sp500` | 165,744 | 460 | 9,492 | 175,696 |

These are safe candidate counts. Parsing determines the actual issuer and can
identify false positives.

## Deferred Work

- Validate Schedule 13D/G recall after the clean rebuild.
- Add Forms 3/5 only when their UI and operator value are clear.
- Treat 13F separately because the master-index CIK identifies the investment
  manager, not every issuer held in its portfolio.
- Reconsider the archive service independently after ownership behavior is
  correct and observable.
