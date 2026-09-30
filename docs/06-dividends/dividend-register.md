# Dividend Register

The dividend register is a company-level derived artifact. It combines local
Form 8-K, 10-Q, and 10-K results instead of creating a separate dividend file
for each accession.

```text
data/parsed/<10-digit CIK>/dividend-view.json
```

The workflow rebuilds this file after processing a filing. The rebuild scans
all parsed filings for that CIK, so processing one new 8-K also refreshes the
register using the existing 10-Q and 10-K history.

## What is recorded

Form 8-K announcement text is the primary source for dividend events. The
extractor records the amount per share, declaration date, ex-dividend date,
record date, payable date, event status, confidence, accession, document, and
evidence text when the announcement uses supported wording.

Form 10-Q and 10-K XBRL facts are reconciliation observations. They record
common-stock dividends declared per share and common-stock cash dividends
paid. Aggregate cash-flow totals are not converted into individual payment
events because that would invent dates or amounts that the filing does not
identify.

The current scope is cash dividends for common stock. Missing dates remain
empty, and a missing 8-K does not mean that a dividend was not declared.

## Download and process one company

First prepare the SEC index if it is not already available:

```bash
go run ./cmd/edgar index --stitch
```

Download and process all three filing types for one CIK:

```bash
go run ./cmd/edgar filings \
  --cik 789019 \
  --from-year 2015 \
  --to-year 2026 \
  --form-type 8-K \
  --form-type 10-Q \
  --form-type 10-K
```

Use 2015 as the recommended lower bound. Use 2016 instead when the company
has no usable 2015 filing or when a shorter history is sufficient.

Restrict the run to a different filing period when needed:

```bash
go run ./cmd/edgar filings \
  --cik 789019 \
  --from-year 2024 \
  --to-year 2026 \
  --form-type 8-K \
  --form-type 10-Q \
  --form-type 10-K
```

The `filings` command downloads missing submissions and processes them into
`data/parsed/`, including `dividend-view.json`.

## Process filings already downloaded

If the submissions already exist in `data/filings/`, process them without
downloading again:

```bash
go run ./cmd/edgar parse \
  --cik 789019 \
  --form-type 8-K \
  --form-type 10-Q \
  --form-type 10-K
```

Use `--reprocess` after parser or dividend-extraction changes:

```bash
go run ./cmd/edgar parse \
  --cik 789019 \
  --form-type 8-K \
  --form-type 10-Q \
  --form-type 10-K \
  --reprocess
```

To process one local SEC submission, use `--file`:

```bash
go run ./cmd/edgar parse \
  --file data/filings/edgar/data/789019/example.txt
```

Processing a single file still rebuilds the complete company register from
all parsed filings already present for that CIK. It cannot be combined with
`--cik` or `--form-type`.

## View the result

Start the local web application after processing:

```bash
go run ./cmd/edgar serve
```

Open a filing detail page. The Dividends panel appears when the company has a
non-empty dividend register. The panel shows announcement events and, when
available, the 10-Q/10-K reconciliation observations.
