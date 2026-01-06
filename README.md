# chopstick

goal
Predicting whether a stock will perform or underperform an index based on a brief number of coefficients.








Data required:

EDGAR data SEC filings. These contain financial statements and other relevant information about publicly traded companies. Even though the data covers mainly US companies, it can provide insights into global market trends. Companies trading at multiple exchanges  can be analyzed using their SEC filings.

the idea is to start with a small number of coefficients, such as:
- Price-to-Earnings (P/E) Ratio
- Return on Equity (ROE)
- Earnings Per Share (EPS)
- Dividend Yield
- Revenue Growth Rate
- Operating Margin
- Total return over a specific period (e.g., 1 year, 3 years)

The exact coefficients should be determined based on their relevance to stock performance and availability in the EDGAR filings. It should be a small number to start with, perhaps 5 coefficients.


first step is to set up a robust data pipeline to extract and preprocess the required data from EDGAR. 

this includes downloading the filings, 10-K and 10-Q forms, parsing the relevant financial statements, and calculating the necessary coefficients. Open and close prices for stocks and indices will also be required to evaluate performance.


EDGAR has a usable API that can be accessed to download the required data. The data can be accesed based on a fair use policy. Main requirement is not to overload the servers with too many requests in a short time frame. 10 requests per second is considered acceptable.  











## Technical considerations.

Decimals vs floats in finance

Deciding between float64 and an alternative (integers or Decimals) is a critical technical choice to make.For an index like the MSCI ACWI, the answer depends on what we are going to do with the data.1. The "Golden Rule" of Financial Dev: "Never use float64 for money". Calculating a portfolio's actual value (e.g., $10,000.50), use integers (representing cents) or a Decimal library.Why? In binary, $0.1 + 0.2$ equals $0.30000000000000004$. Over 2,500 holdings, these tiny errors accumulate, leading to "ghost pennies" that break your balance sheets.

| Scenario           | Recommended Type | Why                                                                 |
|--------------------|------------------|---------------------------------------------------------------------|
| Storing Weights    | int64 (Scaled)  | Store 0.05% as 5000 (basis points). It's fast, fits in any DB, and avoids all precision issues. |
| Simple Analytics   | float64         | If you are just making a "Tech Exposure" dashboard where 36.0000001% is "close enough," floats are fine and 10x faster. |
| Regulated Reports  | Decimal         | If you need to match MSCI's official factsheets exactly, use shopspring/decimal. It handles rounding rules (e.g., Round Half Up) predictably. |


### GOLANG DECIMAL LIBRARIES
Go's standard library does not include a built-in decimal type; instead, it relies on external, third-party libraries to handle arbitrary-precision decimal arithmetic. These libraries are widely used, especially in financial or other applications where floating-point inaccuracies are unacceptable. 
Several high-quality, production-ready decimal libraries are available for Go: 
shopspring/decimal: 

This is one of the most widely used and popular general-purpose libraries, known for its idiomatic Go API where methods return new Decimal values rather than modifying existing ones. It handles addition, subtraction, multiplication with no precision loss, and division with specified precision, and includes support for database/SQL and JSON serialization.

cockroachdb/apd: Developed by CockroachDB, this library is faster than shopspring/decimal and implements the General Decimal Arithmetic specification.  
ericlagergren/decimal: This high-performance library also follows the math/big API style and aims to be consistently one of the fastest arbitrary-precision floating-point libraries available, regardless of the programming language. 
 

 for this project, shopspring/decimal is a good choice due to its balance of performance, ease of use, and community support. It is well-suited for financial calculations where precision is critical.


