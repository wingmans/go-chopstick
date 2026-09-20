# Official SEC References

The project treats SEC EDGAR submissions as the primary source. These links
are the starting points for checking filing identity, filing contents, and
XBRL behavior.

## Filing Search

- [SEC EDGAR search](https://www.sec.gov/edgar/search/): search filings by
  company, form type, date, or filing text.
- [SEC company search](https://www.sec.gov/edgar/searchedgar/companysearch):
  inspect a company's filing history and CIK.
- [SEC filing index](https://www.sec.gov/Archives/edgar/full-index): browse
  the quarterly full-index archives used by the downloader.

## XBRL And Structured Data

- [SEC XBRL resources](https://www.sec.gov/structureddata): SEC guidance and
  background for structured financial data.
- [SEC XBRL facts](https://www.sec.gov/edgar/sec-api-documentation): official
  API documentation and filing-data access patterns.
- [SEC Inline XBRL viewer](https://www.sec.gov/ixviewer/doc/action?doc=):
  the SEC viewer entry point for inline XBRL documents.

## How The Links Are Used Here

- Use the filing search to confirm the company, form, filing date, and
  accession number.
- Use the filing index to verify that a local download corresponds to the
  SEC-relative path recorded in `master.tsv`.
- Use the filing itself, not a vendor label, to confirm a concept, period,
  unit, sign, and context.
- Use the XBRL resources to understand the reporting format; the local parser
  still preserves the filing as the evidence for a particular value.

The official SEC material explains the source format. Project decisions about
normalization, compact views, taxonomy aliases, and accepted exceptions are
recorded in the neighboring project notes.
