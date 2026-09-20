# EDGAR Pipeline

The current flow, from SEC EDGAR to persisted parsed data:

```mermaid
flowchart LR
    A[SEC EDGAR] --> B[Download indexes] --> C[Build master index] --> D[Download filings] --> E[Parse filings] --> F[Parsed data]
```

| Step | Input | Output | CLI command |
| --- | --- | --- | --- |
| **SEC EDGAR** | Internet service | Quarterly index ZIPs and filing documents | |
| **Download indexes** | SEC quarterly indexes | `data/cache/index-zips/` and `data/indexes/quarterly/` | `edgar index` |
| **Build master index** | Quarterly TSV files | `data/indexes/master.tsv` | `edgar index --stitch` |
| **Download filings** | `master.tsv` and SEC filing paths | `data/filings/` | `edgar filings` |
| **Parse filings** | Local `data/filings/` and `master.tsv` | `data/parsed/` | `edgar parse` |
| **Parsed data** | Parsed submissions and XBRL facts | Persisted `filing.json` files | |

## Normal Flow

```text
SEC EDGAR -> indexes -> master.tsv -> filings -> parsed -> data/parsed/
```

`index` downloads and prepares the catalog. `filings` selects records from the
catalog, downloads missing submissions, and can process them. `parse` starts
with local files and processes filings that do not yet have reusable parsed
output.

The usual filters are applied during filing selection:

```text
master.tsv --year --cik --form-type --> selected filings
```

`--noop` shows the planned work without downloading or writing. `--reprocess`
parses again even when existing parsed output is reusable.
