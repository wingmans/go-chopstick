# ETF

Chopstick aims to predict stock performance against global indices. To evaluate this, we need reliable ETF data representing these indices.  Therefore it is essential to ***automaticaly*** download and process ETF data for major global indices, or any ETF we might choose later.

MSCI and other indexes are not directly sharing ALL their index data for free. However, many ETFs track these indices and provide a practical way to access this information. For our analysis purposes, we can use ETF data that has a refresh frequency of daily or even weekly. 



| ETF Ticker   | Index Tracked          | Region           | Product ID |
|--------------|------------------------|------------------|------------|
| ACWI         | MSCI ACWI             | US (Global)       | 239600     |
| URTH         | MSCI World            | US (Developed)    | 239696     |
| IWDA / SWDA  | MSCI World            | UK/EU (Developed) | 251882     |
| EEM          | MSCI Emerging Markets | US (Emerging)     | 239637     |
| IEMG         | MSCI EM IMI           | US (Emerging)     | 244050     |



There are many ETFs tracking these indices. Here are some examples:
https://www.ishares.com/us/products/239600/ishares-msci-acwi-etf
https://www.ishares.com/uk/individual/en/products/251882/ishares-core-msci-world-ucits-etf

One of the tasks to do is to set up a professional robust data pipeline to download and process the ETF data for these tickers. The data should include at least the following fields: data ans share symbol. open/close prices, volume, dividends, splits. will come from  another source. When this info is easily avalable it can be added later as a means to cross-check data accuracy.

## Comparing ETF Data Providers

### Scraping iShares ETF Holdings Data

The Holdings section for iShares looks promising as a source of additional data:
https://www.ishares.com/us/products/239600/ishares-msci-acwi-etf/1467271812596.ajax?fileType=csv&fileName=ACWI_holdings&dataType=fund


### Constructing the Download URL
Preliminary research indicates that iShares uses specific product IDs in their URLs to identify different ETFs. Here are the product IDs for some popular ETFs:
The  IDs can be used to build a dynamic URL in Go code. For U.S.-listed funds, the pattern is:

 For example,  the ACWI download link can be assembled as follows:
    [ID] = 239600
    [TICKER] = ACWI

    https://www.ishares.com/us/products/[ID]/fund-name/1467271812596.ajax?fileType=csv&fileName=[TICKER]_holdings&dataType=fund
    
    This results in this url: https://www.ishares.com/us/products/239600/ishares-msci-acwi-etf/1467271812596.ajax?fileType=csv&fileName=ACWI_holdings&dataType=fund

### Key Challenges & Tips
Variable Column Indices
Depending on the iShares region (US vs. UK), the "Weight" might be in column 5 or 7. To handle this, we can read the CSV header row first and dynamically determine the index of the "Weight" column. This makes the scraper more robust to changes in the CSV structure.

Character Encoding
Occasionally, international iShares sites use non-UTF8 encodings. The golang.org/x/text/encoding package is more than able to handle this if needed.

Rate Limiting
Scrape multiple funds (e.g., all 400+ iShares ETFs), add a time.Sleep(2 * time.Second) between requests. BlackRock’s servers may temporarily block your IP if they detect high-frequency automated downloads. This is especially important when planning to download data for many ETFs in a loop. Even a minute delay can help avoid being rate-limited or blocked. This is feasible for Chopstick since ETF holdings change infrequently (monthly or quarterly).
 