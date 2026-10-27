# Error Handling Philosophy

The project uses ordinary Go errors inside the application and structured
errors only when an error crosses a process or service boundary.

## Core Rule

Wrap errors freely while preserving their cause. Classify an error once, as
late as possible, at the boundary that must give it meaning.

Parser, filesystem, storage, and workflow packages should return ordinary
errors with useful context. They should not know whether the caller is a CLI,
HTTP server, background job, or future event consumer. Existing standard
sentinels such as `os.ErrNotExist`, `io.EOF`, `context.Canceled`, and
`context.DeadlineExceeded` should be reused when they describe the condition.

The `internal/xerr` package provides the small structured layer for
boundaries. Its stable code describes the category, its reason identifies the
operation, its message is suitable for people, and its cause preserves
debugging information. Details are operator-facing metadata, not a second
control-flow mechanism.

## Boundary Policy

- The CLI classifies invalid input, missing local data, cancellation, timeouts,
  dependency failures, and unexpected failures. Help is a successful
  non-error path and remains separate.
- The HTTP server maps missing resources to `404`, invalid requests to `400`
  when applicable, and unexpected failures to a generic `500` response.
  Detailed causes are logged rather than returned to the browser.
- The filing workflow continues processing a batch after an individual filing
  fails, logs each failure, and returns a summary that preserves the
  underlying causes.
- The parser records extraction limitations in filing diagnostics. It returns
  a Go error for failures that prevent a usable parse; it does not classify
  those failures as HTTP or CLI errors.

Structured errors are not required for every function and should not replace
contextual `fmt.Errorf("...: %w", err)` wrapping.

## Stable Codes

The current boundary categories are `NOT_FOUND`, `INVALID_INPUT`, `CONFLICT`,
`INTERNAL`, `UNAVAILABLE`, `DEADLINE_EXCEEDED`, and `CANCELED`. These codes are
stable machine-readable categories. Human messages and implementation causes
may change.

## Future Improvements

As storage, eventing, and background jobs are added, each boundary can map the
same underlying causes to its own protocol without coupling the core packages
to that protocol. Future work may add richer HTTP status mapping, explicit
upstream HTTP error types, exit-status selection for the CLI, and structured
batch error reporting. Those should be introduced only when a real caller
needs them.

## Linter Policy

Correctness and boundary-safety checks should remain enabled: `errcheck`,
`errorlint`, `errchkjson`, `contextcheck`, `noctx`, `nilerr`, `nilnil`,
`gosec`, and `exhaustruct_v5`. These catch ignored failures, broken wrapping,
unsafe responses, lost contexts, invalid values, security risks, and incomplete
struct literals.

`revive`, `govet`, `staticcheck`, `unconvert`, and modernize checks should stay
enabled. Complexity, length, dependency-policy, and naming/style linters can
remain disabled until the codebase has a consistent baseline. In particular,
`err113` should remain disabled temporarily: introducing a sentinel for every
parser message would make the error model worse. Enable it incrementally after
caller-visible sentinels have been identified.

`wrapcheck` should likewise be enabled incrementally at package boundaries.
Requiring wrapping for every low-level return adds noise where the original
standard-library error already has the correct meaning.
