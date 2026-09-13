# xerr

`xerr` is a small structured error type for process and service boundaries. It
is not a replacement for ordinary Go errors inside the application.

## Rule

Wrap errors freely while preserving their cause. Classify an error once, as
late as possible, at the boundary that must give it meaning.

Use normal errors in parsers, filesystem code, storage, and internal helpers:

```go
return fmt.Errorf("read filing %s: %w", path, err)
```

At a boundary, convert the final error into an `xerr.Error`:

```go
return xerr.Wrap(xerr.Internal, "FILING_READ_FAILED", "could not read filing", err)
```

The cause remains available through `errors.Is` and `errors.As`.

## Codes

Codes are stable machine-readable categories: `NotFound`, `InvalidInput`,
`Conflict`, `Internal`, `Unavailable`, `DeadlineExceeded`, and `Canceled`.

The `Reason` identifies the operation. The `Message` is human-readable. Neither
should be used as a substitute for inspecting the wrapped cause.

## Details

Details are operator-facing metadata. They are copied onto a new error and do
not mutate the original:

```go
return xerr.Wrap(xerr.Unavailable, "EDGAR_UNAVAILABLE", "EDGAR service is unavailable", err).
    WithDetails(map[string]any{"url": requestURL})
```

Details are not intended for control flow and should not contain secrets.

## Boundary mappings

- CLI: invalid input, missing local data, dependency failures, cancellation, timeout, or internal failure.
- HTTP: `NOT_FOUND` to 404, invalid input to 400, unavailable dependencies to 503, and unexpected failures to 500.
- Jobs and event consumers: retain the stable code and cause for retry or failure decisions.

Help output is a successful CLI path and should remain separate from errors.
