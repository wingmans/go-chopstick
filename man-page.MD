# Chop Man Pages

## Goal

Add professional Unix man pages for the Chop operator CLI and daemon without
creating a second, manually maintained command parser.

The man pages should explain application concepts, operational boundaries,
files, environment variables, lifecycle, and representative workflows. Kong
remains authoritative for complete command and flag syntax.

## Initial Scope

Create two section-one man pages:

- `chop(1)` for the operator CLI.
- `chopd(1)` for the foreground application process.

Do not initially create a separate page for every subcommand. Pages such as
`chop-index(1)` can be added later if a command develops enough conceptual
documentation to justify one.

Do not add `chop start` or `chop stop`. Development process lifecycle remains
owned by the shell, and deployed process lifecycle will belong to a service
manager such as systemd.

## Source and Generated Files

Use readable Markdown as the maintained source:

```text
docs/man/chop.1.md
docs/man/chopd.1.md
```

Convert the sources to Unix roff with a pinned Go documentation tool such as
`go-md2man`.

Generate:

```text
dist/man/man1/chop.1
dist/man/man1/chopd.1
```

Keep all maintained Markdown wrapped to 80 columns. Do not maintain roff by
hand.

Decide during implementation whether generated roff should be checked in. The
preferred starting point is to check it in because it is small and allows
release packaging without installing the documentation generator.

## `chop(1)` Contents

Include these sections:

```text
NAME
SYNOPSIS
DESCRIPTION
COMMANDS
ONLINE AND OFFLINE OPERATIONS
INDEX MODEL
ENVIRONMENT
FILES
EXIT STATUS
EXAMPLES
DIAGNOSTICS
SEE ALSO
```

Document the public command families at a useful summary level:

- `doctor`
- `status`
- `index download`
- `index import`
- `index status`
- `failures`
- `filings`
- `ownership`
- `rebuild`

Explain concepts that do not fit comfortably in command help:

- The difference between `chop` and `chopd`.
- Which commands require a running daemon.
- Which operations require the daemon to be stopped.
- Available, stitched, imported, selected, and processed data.
- Why `EDGAR_SET` and form types are downstream selection policy.
- How year selectors limit development history.
- Where application data is stored.
- How NATS relates to Chop.
- What rebuild removes and preserves.

For exhaustive syntax, direct users to:

```text
chop <command> --help
```

## `chopd(1)` Contents

Include these sections:

```text
NAME
SYNOPSIS
DESCRIPTION
STARTUP
CONFIGURATION
NETWORK ENDPOINTS
STORAGE
SIGNALS
LOGGING
ENVIRONMENT
FILES
DEVELOPMENT
SERVICE MANAGEMENT
DIAGNOSTICS
SEE ALSO
```

Document these behaviors:

- Running `chopd` without arguments starts the configured application.
- `chopd --help` displays command help.
- `SIGINT` and `SIGTERM` request graceful shutdown.
- Development uses a foreground process and `Ctrl-C`.
- A future systemd unit will run the same foreground executable.
- NATS remains an independent local service.
- The HTTP, command, and NATS endpoints have separate responsibilities.
- `CHOPSTICK_DATA_DIR` owns application storage and the application lock.
- Log levels can be configured through flags and environment variables.

## Avoiding Command Drift

Kong remains the source of truth for command names, flags, aliases, defaults,
enums, and validation.

The initial man pages should contain concise command summaries and stable
examples rather than duplicating every flag. If drift becomes a practical
problem, add generation of the `COMMANDS` and `OPTIONS` sections from the Kong
model as a separate improvement.

Do not build a Kong-to-man generator in the first implementation.

## Build and Preview

Add coding-focused Make targets:

```text
make man
make check-man
```

`make man` generates the roff pages. `make check-man` regenerates them and
fails when checked-in output differs.

Document local preview. Depending on the host implementation, this will be
one of:

```bash
man ./dist/man/man1/chop.1
man -l ./dist/man/man1/chop.1
```

Do not install into a system directory as part of generation or tests.

## Installation and Packaging

Man pages become globally discoverable only when release packaging installs
them under a man path such as:

```text
/usr/local/share/man/man1/
```

`go install` does not install accompanying man pages. Installation belongs to
future release archives, a package manager such as Homebrew, or an explicit
installation target.

The build should not require elevated privileges.

## Verification

Add checks that cover:

- Both Markdown sources generate valid, non-empty roff pages.
- Every public top-level `chop` command appears in `chop(1)`.
- `chopd(1)` documents foreground startup and graceful shutdown.
- Generated pages are current when checked in.
- Maintained Markdown respects the 80-column source limit.
- `git diff --check` succeeds.

Run the normal repository checks as applicable:

```bash
go test ./...
golangci-lint run ./cmd/... ./internal/...
make check-generated
make check-man
git diff --check
```

Do not add lint suppressions for documentation tooling.

## Completion Criteria

The task is complete when:

- `chop(1)` and `chopd(1)` have readable Markdown sources.
- The pages explain concepts and lifecycle rather than merely copying help.
- A pinned tool produces portable section-one man pages.
- Developers can generate, verify, and preview the pages locally.
- Release packaging has a documented installation location.
- Command syntax remains owned by Kong.
- No process manager, installer framework, or command generator has been
  introduced unnecessarily.

## Effort

Expected initial effort is approximately one working day:

- One to two hours for tooling and Make targets.
- Two to three hours for `chop(1)`.
- One to two hours for `chopd(1)`.
- About one hour for checks and packaging documentation.

Ongoing maintenance should normally take only a few minutes when command
behavior changes.
