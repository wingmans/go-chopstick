# SEC XML encoding

The SEC `getcurrent` Atom feed may declare `ISO-8859-1` encoding. Go's XML
decoder does not handle non-UTF-8 encodings by default and returns:

```text
xml: encoding "ISO-8859-1" declared but Decoder.CharsetReader is nil
```

The polling fetcher configures `xml.Decoder.CharsetReader` with
`golang.org/x/net/html/charset` so the declared encoding is converted while
the feed is decoded.

## Accession URL format

SEC commonly places accessions in feed links as an 18-digit value:

```text
.../edgar/data/320193/000032019326000001-index.htm
```

The normalized accession format used by the application is:

```text
0000320193-26-000001
```

The fetcher accepts both URL formats and normalizes the numeric form. If the
accession format is not recognized, the entry is skipped. DEBUG logging reports
the total entries, normalized events, and skipped entries:

```text
SEC Atom feed parsed entries=100 events=100 skipped=0
```
