# ETF

## Comparing datasources for ETF constituents (holdings) data

ETF data for global indices can be obtained from various financial data providers. Here we compare three options:

- iShares ETF Holdings Scraping (Free, Manual)
- Financial Modeling Prep (FMP) API (Freemium)
- EOD Historical Data (EODHD) API (Paid)

EOD Historical Data (EODHD) provides comprehensive financial data, including ETF holdings, historical prices, and corporate 
actions. They offer a robust API that can be used to fetch ETF holdings data programmatically. For now tihis one is out of scope due to cost.

|  Feature           |    iShares (scrape)     | FMP (Freemium)         | EODHD (Paid)                   |
|--------------------|-------------------------|------------------------|--------------------------------|
| ACWI Coverage      |    Excellent            | Excellent             | Excellent                       |
| Data Format        |    CSV                  | Flat JSON (Easy)      | Deeply Nested JSON (Complex)    |
| Primary Strength   |    Free, the source     | Speed of implementation | Historical accuracy & Support |
| Pricing            |    Free                 | Free tier available   | ~$20/mo starting                |

iShares might be brittle of fragile when iShares changes their HTML structure. 
FMP is easy to implement but has rate limits and a freemium model. 
EODHD is paid but offers robust support and historical data.


| Step         | iShares Scraper (Manual)           | API (EODHD/FMP)                             |
|--------------|------------------------------------|---------------------------------------------|
| Ingestion    | http.Get on a hidden AJAX link    | http.Get on a documented REST endpoint       |
| Parsing      | csv.NewReader + Row Skipping      | json.NewDecoder + Struct Unmarshalling       |
| Maintenance  | High: Breaks if CSV columns move  | Low: API contract is stable                  |
| Metadata     | Minimal (Ticker, Name, Weight)    | Rich (Sector, Region, P/E ratio, Market Cap) |


### Conclusion
We will try iShares scraping **first** for quick prototyping.
If it proves too brittle, we can switch to FMP or even (paid) EODHD APIs for more robust data access. When implementing the scraper, we should build in error handling to detect changes in the HTML structure and alert us when the scraper breaks. Using 
FMP freemium to crosscheck data accuracy is also a good idea. We will prepare for future migration that the underlying data source may change.



### Implementation Details for iShares Scraper

Preliminary research indicates that iShares uses specific product IDs in their URLs to identify different ETFs. Here are the product IDs for some popular ETFs:
The  IDs can be used to build a dynamic URL in Go code. For U.S.-listed funds, the pattern is:

 For example,  the ACWI download link can be assembled as follows:
    [ID] = 239600
    [TICKER] = ACWI

    https://www.ishares.com/us/products/[ID]/fund-name/1467271812596.ajax?fileType=csv&fileName=[TICKER]_holdings&dataType=fund
    
    This results in this url: https://www.ishares.com/us/products/239600/ishares-msci-acwi-etf/1467271812596.ajax?fileType=csv&fileName=ACWI_holdings&dataType=fund


    Variable Column Indices
    Depending on the iShares region (US vs. UK), the "Weight" might be in column 5 or 7. To handle this, we can read the CSV header row first and dynamically determine the index of the "Weight" column. This makes the scraper more robust to changes in the CSV structure.

    Character Encoding
    Occasionally, international iShares sites use non-UTF8 encodings. The golang.org/x/text/encoding package is more than able to handle this if needed.

    Rate Limiting
    Scrape multiple funds (e.g., all 400+ iShares ETFs), add a time.Sleep(2 * time.Second) between requests. BlackRock’s servers may temporarily block your IP if they detect high-frequency automated downloads. This is especially important when planning to download data for many ETFs in a loop. Even a minute delay can help avoid being rate-limited or blocked. This is feasible for Chopstick since ETF holdings change infrequently (monthly or quarterly).


## Implementation Details for FMP api for ETF holdings

FMP Endpoint: https://financialmodelingprep.com/api/v3/etf-sector-weightings/ACWI?apikey=YOUR_KEY
Go obtain an apikey so we can test it out.

https://site.financialmodelingprep.com/

```go

type Holding struct {
    Asset    string  `json:"asset"`
    Symbol   string  `json:"symbol"`
    Weight   float64 `json:"weightPercentage"`
    Shares   float64 `json:"sharesNumber"`
}''

// Fetching ACWI holdings via FMP API
url := "https://financialmodelingprep.com/api/v3/etf-holder/ACWI?apikey=YOUR_KEY"
resp, _ := http.Get(url)
var holdings []Holding
json.NewDecoder(resp.Body).Decode(&holdings)``

 

Use a Registry Pattern so each source could have a Parse() method that returns your standardized []Holding. This allows 
us to add a 4th or 5th source later without touching your breakdown logic.

// Parser is the interface every ingestion source must implement
type Parser interface {
	// Identity of the source (e.g., "iShares", "FMP")
	SourceID() string
	
	// Fetch and transform raw data into our clean internal struct
	FetchLatest() (*IndexSnapshot, error)
}

// IndexSnapshot is the industry-appropriate top-level container
type IndexSnapshot struct {
	IndexName    string        `json:"index_name"`    // MSCI ACWI
	LastUpdated  time.Time     `json:"last_updated"`
	
	// We use "Constituents" for the raw list
	Constituents []Constituent `json:"constituents"`
}

type Constituent struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Weight   int64  `json:"weight"` // Scaled int
	Sector   string `json:"sector"`
	Country  string `json:"country"`
}



## Nomenclature Hierarchy
In professional finance, we distinguish between the Index (the abstract math) and the Fund (the actual bucket of money).

| Term              | Professional Context         | Meaning in your Go App                                     |
|-------------------|------------------------------|------------------------------------------------------------|
| Constituents      | Index Level                  | The specific stocks that make up the index                 |
|                   |                              | (e.g., Apple is a constituent of MSCI ACWI).               |
| Holdings          | Fund Level                   | The actual shares owned by an ETF like iShares ACWI.       |
|                   |                              | In a perfect world, Holdings = Constituents.               |
| Asset Allocation  | Strategy Level               | The split between broad categories like Equities, Bonds,   |
|                   |                              | and Cash.                                                  |
| Exposure / Dice   | Risk Level                   | The breakdown by specific traits: Sector, Geography,       |
|                   |                              | or Currency.                                               |
|---------------------------------------------------------------------------------------------------------------|


"Asset Allocation" vs. "Sector Breakdown"
Industry-standard reports (like BlackRock or Vanguard factsheets) usually separate these into two distinct sections:

- Asset Allocation: Always adds up to 100%. (e.g., 98% Stocks, 2% Cash).  
- Sector/Region Exposure: Dices that 98% of Stocks into categories (e.g., 27% Tech, 15% Finance).



## Considerations for Internal Data Representation
To be as future proof as possible, the goal is to ***decouple***. We seek to separate the "Dirty Ingestion" layer (the logic 
that handles different API/Scraper quirks) from the "Clean Core" (the logic that does the breakdowns).

The ingestion layer stores all raw data as-is, while the core layer works with a standardized internal representation. This way we can swap out data sources or add new ones without changing the core logic. 
Storing the raw data makes it possible to reprocess it later if needed.

To achieve this we must create a Standardized Internal Representation. 


### multiple classification standards (GICS, ICB, TRBC) may be in use simultaneously.
To handle scrapers and multiple APIs while remaining "GICS-compliant," our Go struct should separate Identity (who they are) from Classification (what they do) and Exposure (their weight).

Various data providers may offer different levels of metadata (e.g., Sector, Industry, Country). Our internal representation should be flexible enough to accommodate this variability while maintaining a consistent structure for analysis.

When fetching data from three possible  sources (iShares, EODHD, and FMP), a Data Normalization challenge arises.

iShares Scraper:    Typically provides the sector as a string (e.g., "Information Technology").
EODHD API:          Often supplies both the GICS Code (e.g., 45) and the corresponding string.
FMP API:            Frequently delivers a simplified category string or an SIC Code (a different, older classification standard).


In order to deal with these we map strings to codes internally
To address this, a "Source of Truth" map should be implemented within the Go application. This map will normalize variations in sector strings (e.g., "IT" vs. "Information Technology") into the official GICS Code. This ensures consistency across data sources and simplifies downstream processing.

Another concept we need to deal with is the changing composition of index over time, eg constituents drop out, go bankrupt or are added to the index?  For our data architecture, the described scenario represents a classic Point-in-Time (PIT) problem. Since indices like the MSCI ACWI are rebalanced regularly (typically on a quarterly basis), overwriting the holdings would result in the loss of historical snapshots, making it impossible to view the composition of the index at a specific point in the past.

This his issue is addressed using Temporal Versioning, commonly referred to as the "Slowly Changing Dimension Type 2" approach. This method ensures that historical data is preserved while allowing for changes over time. This makes our data ingestion pipeline a bit more complex, as we need to check for changes in the holdings and create new records with effective dates rather than simply updating existing ones. Implementing this approach allows us to maintain a complete history of index compositions, which is crucial for accurate backtesting and performance analysis. 

The "Snapshot-in-Time" Schema
Instead of updating a single list, you treat every "fetch" as a unique, immutable version of that index. You identify these versions using an EffectiveDate or a VersionID.



Comparison of Industry Classification Standards.

| Standard | Owner         | Top Level Name         | Hierarchy                          |
|----------|---------------|------------------------|-------------------------------------|
| GICS     | MSCI / S&P    | Sector (e.g., IT)      | 4 Tiers (2, 4, 6, 8 digits)        |
| ICB      | FTSE / Russell| Industry (e.g., Tech)  | 4 Tiers (Used by NASDAQ/LSE)       |
| TRBC     | LSEG / Reuters| Economic Sector        | 5 Tiers (Most granular)            |
| Custom   | You           | Theme / Tag            | Flat or Nested (Flexible)          |



GICS Sector Codes Reference.

| Sector Name            | GICS Sector Code | Key Sub-Industry Code (Example) |
|------------------------|------------------|----------------------------------|
| Energy                 | 10               | 10102010 (Integrated Oil & Gas) |
| Materials              | 15               | 15101010 (Commodity Chemicals)  |
| Industrials            | 20               | 20101010 (Aerospace & Defense)  |
| Consumer Discretionary | 25               | 25102010 (Automobile Manufacturers) |
| Consumer Staples       | 30               | 30101010 (Drug Retail)          |
| Health Care            | 35               | 35202010 (Pharmaceuticals)      |
| Financials             | 40               | 40101010 (Diversified Banks)    |
| Information Technology | 45               | 45203015 (Electronic Components) |
| Communication Services | 50               | 50201010 (Advertising)          |
| Utilities              | 55               | 55101010 (Electric Utilities)   |
| Real Estate            | 60               | 60101010 (Diversified REITs)    |




structs in GO to deal with Index Constituents with Temporal Versioning

``go
// 1. INDEX DEFINITION (The "Bucket")
// Represents the Index itself (MSCI ACWI, S&P 500, or "AI Custom Index").
type IndexDefinition struct {
	ID          string `json:"id"`           // e.g., "msci-acwi"
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`     // e.g., "MSCI", "User_AI"
}

// 2. SECURITY MASTER (The "Identity")
// Represents the company as a permanent global entity.
type SecurityMaster struct {
	ISIN      string `json:"isin"`       // The primary unique key
	CUSIP     string `json:"cusip"`      // Regional ID
	PrimaryTicker string `json:"ticker"` // Current ticker
}

// 3. INDEX SNAPSHOT (The "Point-in-Time" Header)
// Represents a specific fetch or rebalance event for a specific index.
type IndexSnapshot struct {
	VersionID     string    `json:"version_id"`     // UUID
	IndexID       string    `json:"index_id"`       // Links to IndexDefinition
	EffectiveDate time.Time `json:"effective_date"` // Date the index rules apply
	CapturedAt    time.Time `json:"captured_at"`    // Physical time of ingestion
}

// 4. CONSTITUENT ENTRY (The "Temporal Link")
// This is the "Slowly Changing Dimension" record.
type ConstituentEntry struct {
	VersionID string `json:"version_id"`        // Links to IndexSnapshot
	ISIN      string `json:"isin"`              // Links to SecurityMaster

	// Data specific to THIS index at THIS specific time
	Ticker    string            `json:"ticker"`         // Ticker at time of snapshot
	Weight    int64             `json:"weight"`         // Scaled integer (8 decimals)
	
	// The "Flex Point" for dicing/classifications
	Labels    map[string]string `json:"labels"`
}
``

Having this structure in place, allows to easily query the index composition at any point in time by joining IndexSnapshot with ConstituentEntry on VersionID. This allows us to reconstruct historical index compositions for backtesting or analysis.

It lets us answer questions like:

- Change Reporting of constituents over time (added/dropped)
- Historical Weight Changes of a specific stock within the index
- Impact of Rebalancing Events on index performance
- Breakdowns:

        | Method Name          | Label Key Used      | Purpose                                                   |
        |----------------------|---------------------|-----------------------------------------------------------|
        | GetSectorWeight      | "GICS_SECTOR"       | Aggregates weights by standard GICS industry sectors.     |
        | GetCountryWeight     | "COUNTRY_ISO"       | Groups by ISO country codes (e.g., "US", "CN").           |
        | GetAssetClassWeight  | "ASSET_CLASS"       | Splits by Equities, Cash, or Fixed Income.                |
        | GetCustomDiceWeight  | "AI_THEME"          | Groups by a user-defined or AI-generated classification.  |


The  constituent table will be the linking pin to other data sources such as financial statements, stock prices, filings of e.g. 10K ,13F  etc. The ISIN serves as the universal key to join with these external datasets.