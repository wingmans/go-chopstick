# Malformed XBRL Fixtures

These files preserve tiny examples of legacy SEC XBRL instance defects seen
in 2010-2011 validation-set filings.

They are intentionally invalid XML. They should not be repaired in place or
used as examples of acceptable source data. Future parser-compatibility work
can use them to test a parser-only normalization copy while keeping pristine
raw filing bytes unchanged.

## Fixture Promotion Policy

Validation runs may identify malformed filings and diagnostic patterns, but
they must not automatically promote whole filings or reduced examples into this
directory. A human reviewer should inspect the validation evidence, approve the
failure pattern, and copy or reduce the fixture deliberately.

Each fixture should stay small, named after the specific source defect, and
paired with a test that explains the current expected behavior. When future
parser work learns to normalize an older XBRL flavor, the test can change from
"rejects malformed input" to "normalizes a parser-only copy" while preserving
the original malformed bytes here.

- `missing-lt-semicolon.xml`: entity reference like `&lt` without `;`.
- `missing-gt-semicolon.xml`: entity reference like `&gt` without `;`.
- `truncated-closing-tag.xml`: invalid text between a closing tag name and
  `>`, matching legacy truncated closing-tag failures.
