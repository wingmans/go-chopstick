# Malformed XBRL Fixtures

These files preserve tiny examples of legacy SEC XBRL instance defects seen
in 2010-2011 validation-set filings.

They are intentionally invalid XML. They should not be repaired in place or
used as examples of acceptable source data. Future parser-compatibility work
can use them to test a parser-only normalization copy while keeping pristine
raw filing bytes unchanged.

- `missing-lt-semicolon.xml`: entity reference like `&lt` without `;`.
- `missing-gt-semicolon.xml`: entity reference like `&gt` without `;`.
- `truncated-closing-tag.xml`: invalid text between a closing tag name and
  `>`, matching legacy truncated closing-tag failures.
