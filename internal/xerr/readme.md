# xerr

## Structured Errors for Boundaries

xerr provides structured, numeric, wrap-friendly errors for use at system boundaries (HTTP, CLI, jobs), inspired by gRPC error semantics but without gRPC.

It is not a replacement for normal Go error handling internally.

## Design principles

- Classify late: assign error codes only at boundaries
- Wrap, don’t replace: preserve causes with Unwrap
- Stable semantics: numeric codes are stable, messages are not
- Idiomatic Go: compatible with errors.Is / errors.As
- Low ceremony: no registries, no global state

## When to use xerr

Use xerr when:

- Returning errors from HTTP handlers
- Returning errors from CLI entrypoints
- Emitting final errors from background jobs
- Crossing a system boundary

## Do not use xerr for

- Repositories
- Parsers
- Filesystem logic
- SQL helpers
- Internal utilities


## Runtime errors at boundaries

xerr is a good fit for runtime failures **only when the error is crossing a boundary**.

Use normal Go errors internally. Convert to xerr at the final boundary:

- HTTP handlers
- CLI entrypoints
- background job runners
- other process or service boundaries

General rule:

- keep the original cause
- classify once, late
- use stable numeric codes
- use details for operators, not control flow

## Network errors

Outbound network failures are usually runtime failures, not domain failures.

Typical mappings:

- `context.DeadlineExceeded` -> `CodeDeadlineExceeded`
- `net.Error` transport failures and timeouts -> `CodeUnavailable`
- unexpected local failures while handling the response -> `CodeInternal`

Example:

```go
func fetchProfile(ctx context.Context, c *http.Client, url string) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return fmt.Errorf("build request: %w", err)
    }

    resp, err := c.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 500 {
        return fmt.Errorf("upstream returned %d", resp.StatusCode)
    }

    return nil
}

func syncProfile(ctx context.Context, c *http.Client, url string) error {
    err := fetchProfile(ctx, c, url)
    if err == nil {
        return nil
    }

    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return xerr.Wrap(
            CodeDeadlineExceeded,
            "UPSTREAM_TIMEOUT",
            "upstream request timed out",
            err,
        )
    default:
        var netErr net.Error
        if errors.As(err, &netErr) {
            return xerr.WithDetails(
                xerr.Wrap(
                    CodeUnavailable,
                    "UPSTREAM_UNAVAILABLE",
                    "upstream service is unavailable",
                    err,
                ),
                map[string]any{
                    "dependency": "profile-api",
                    "url":        url,
                },
            )
        }

        return xerr.Wrap(
            CodeInternal,
            "PROFILE_SYNC_FAILED",
            "profile sync failed",
            err,
        )
    }
}
```

Notes:

- do not classify inside low-level network helpers
- return raw errors from helpers
- classify once when returning from the boundary

## CLI errors

CLI entrypoints are another strong boundary for xerr.

Typical mappings:

- bad flags or missing arguments -> `CodeInvalidArgument`
- missing config or invalid environment state -> `CodeFailedPrecondition`
- network dependency failures -> `CodeUnavailable`
- unexpected runtime failures -> `CodeInternal`

Example:

```go
func run(ctx context.Context, args []string) error {
    if len(args) == 0 {
        return xerr.New(
            CodeInvalidArgument,
            "COMMAND_REQUIRED",
            "missing command",
        )
    }

    cfg, err := loadConfig()
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return xerr.Wrap(
                CodeFailedPrecondition,
                "CONFIG_MISSING",
                "config file not found",
                err,
            )
        }

        return xerr.Wrap(
            CodeInternal,
            "CONFIG_LOAD_FAILED",
            "failed to load configuration",
            err,
        )
    }

    if err := execute(ctx, cfg, args); err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return xerr.Wrap(
                CodeDeadlineExceeded,
                "COMMAND_TIMEOUT",
                "command timed out",
                err,
            )
        }

        var netErr net.Error
        if errors.As(err, &netErr) {
            return xerr.Wrap(
                CodeUnavailable,
                "DEPENDENCY_UNAVAILABLE",
                "required network dependency is unavailable",
                err,
            )
        }

        return xerr.Wrap(
            CodeInternal,
            "COMMAND_FAILED",
            "command failed",
            err,
        )
    }

    return nil
}

func main() {
    if err := run(context.Background(), os.Args[1:]); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

Notes:

- CLI code is a good place to convert runtime errors into stable codes
- keep messages human-readable
- keep causes wrapped for debugging
- add details when operators need more context

## samples

```go
// wrap
cfg, err := loadConfig()
if err != nil {
	return errors.Wrap(
		CodeInvalidArgument,
		"CONFIG_INVALID",
		"configuration is invalid",
		err,
	)
}

func loadConfig() error {
	b, err := os.ReadFile("cfg.json")
	if err != nil {
		return err // or fmt.Errorf("read cfg.json: %w", err)
	}
	
}


// with metadata
return errors.WithDetails(
	errors.New(
		CodeInvalidArgument,
		"INVALID_PRICE",
		"price must be positive",
	),
	map[string]any{
		"field": "price",
		"value": price,
	},
)

```