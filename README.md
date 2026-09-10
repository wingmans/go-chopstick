# go-ecb

`go-ecb` fetches exchange-rate time series from the European Central Bank (ECB) Data API and writes them to standard output as text, JSON, or CSV.

## Requirements

- Go 1.24 or newer
- Internet access to the ECB Data API

## Quick Start

Run with the default daily USD-to-EUR series:

```bash
make run
```

Build a local executable:

```bash
make build
./bin/ecb
```

## Inputs

The command accepts flags and environment variables. A flag takes precedence over its environment variable.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `-c`, `--currency` | `ECB_CURRENCY` | `USD` | Currency to convert |
| `-n`, `--currency-denom` | `ECB_CURRENCY_DENOM` | `EUR` | Denomination currency |
| `-s`, `--start` | `ECB_START_PERIOD` | One month ago | First observation date, `YYYY-MM-DD` |
| `-e`, `--end` | `ECB_END_PERIOD` | Today | Last observation date, `YYYY-MM-DD` |
| `-o`, `--output` | `ECB_OUTPUT` | `text` | Output format: `text`, `json`, or `csv` |

The ECB dataset is fixed to `EXR`, and requests use a fixed 10-second timeout.

Use `-h` or `--help` to print the command syntax and available options.

The exchange-rate series key is built from the two currency inputs as `D.<currency>.<currency-denom>.SP00.A`. The default is daily (`D`) USD/EUR data.

The SEC EDGAR filing-index downloader is available as a separate command:

```bash
go run ./cmd/edgar -from-year 2025 -user-agent "My Company contact@example.com"
```

It writes one `YYYY-QTRn.tsv` file per quarter to `./data` by default. Use `-refresh-latest` to download the newest quarter again while reusing older files. Use `-stitch` to concatenate the quarterly files into `master.tsv`. Each row includes the original filing path and its `-index.html` path.

The `-end` flag is optional. When omitted, it defaults to the current date.

Common currency codes:

| Currency | Code |
| --- | --- |
| US dollar | `USD` |
| Canadian dollar | `CAD` |
| Brazilian real | `BRL` |
| Swiss franc | `CHF` |
| Swedish krona | `SEK` |
| Danish krone | `DKK` |

Examples:

```bash
go run ./cmd/ecb \
  -currency GBP \
  -currency-denom EUR \
  -start 2025-01-01 \
  -end 2025-01-31
```

Using environment variables:

```bash
export ECB_CURRENCY="GBP"
export ECB_CURRENCY_DENOM="EUR"
export ECB_START_PERIOD="2025-01-01"
export ECB_END_PERIOD="2025-01-31"
export ECB_OUTPUT="json"
go run ./cmd/ecb
```

## Outputs

Output is written to standard output, so it can be redirected or piped to another command.

### Text

```bash
go run ./cmd/ecb -output text
```

```text
Currency Pair:
  FREQ: D
  CURRENCY: USD
  CURRENCY_DENOM: EUR
  EXR_TYPE: SP00
  EXR_SUFFIX: A
Observations:
  2024-07-01 = 0.9300
```

### JSON

```bash
go run ./cmd/ecb -output json > rates.json
```

```json
{
  "dataset": "EXR",
  "series": [
    {
      "series_key": {
        "CURRENCY": "USD",
        "CURRENCY_DENOM": "EUR",
        "EXR_SUFFIX": "A",
        "EXR_TYPE": "SP00",
        "FREQ": "D"
      },
      "observations": [
        {
          "time": "2024-07-01",
          "value": 0.93
        }
      ]
    }
  ]
}
```

### CSV

```bash
go run ./cmd/ecb -output csv > rates.csv
```

CSV includes the series index, observation time, value, and series metadata columns:

```csv
series_index,time,value,CURRENCY,CURRENCY_DENOM,EXR_SUFFIX,EXR_TYPE,FREQ
0,2024-07-01,0.93,USD,EUR,A,SP00,D
```

## ECB Data API

The application requests data from the ECB SDMX REST API using this endpoint shape:

```text
https://data-api.ecb.europa.eu/service/data/EXR/{series}?startPeriod={start}&endPeriod={end}
```

For the default currencies and a bounded date range, an equivalent request is:

```text
https://data-api.ecb.europa.eu/service/data/EXR/D.USD.EUR.SP00.A?startPeriod=2025-01-01&endPeriod=2025-01-31
```

See the [ECB Data API documentation](https://data.ecb.europa.eu/help/api/data) for dataflow identifiers, series keys, query parameters, and response formats. The [ECB API examples](https://data.ecb.europa.eu/help/api/data-examples) include more series and date-range queries.

## Errors

Invalid inputs and failed ECB requests are reported on standard error and return a non-zero exit status. Requests can be canceled with `Ctrl-C`.

## Development

```bash
make build    # Build bin/ecb
make test     # Run all tests
make lint     # Run golangci-lint
make run      # Run the application
make clean    # Remove build artifacts
make release  # Create a Goreleaser release
```
