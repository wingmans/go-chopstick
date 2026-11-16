# Templ diagnostics in VS Code

VS Code reported this error in `layout.templ` and `sidebar.templ`:

```text
undefined: templruntime.NewAttributeExpression
```

The templates were valid. The error was caused by a version mismatch between
the Templ language server used by VS Code and the Templ runtime in `go.mod`.
VS Code found a newer global `templ` executable, while this repository pins
both its generator and runtime to version `v0.3.1001`.

The language server generates temporary Go code for editor diagnostics. The
newer generator emitted a call to `templruntime.NewAttributeExpression`, but
that function was not available in the repository's older runtime. Running
`templ fmt` did not help because formatting does not change generator or
runtime versions.

We fixed the error by configuring the VS Code Templ extension to use the
repository-pinned executable. Add this setting to `.vscode/settings.json`,
using the absolute path to the current checkout:

```json
{
  "templ.executablePath":
    "/absolute/path/to/chopstick/.tools/bin/templ"
}
```

After changing the setting, run `Templ: Restart Language Server` from the VS
Code command palette. If diagnostics remain cached, run
`Developer: Reload Window`.

Verify that the generator and runtime versions match:

```bash
.tools/bin/templ version
go list -m -f '{{.Version}}' github.com/a-h/templ
```

Both commands should print the same version. Prefer the repository executable
for generation as well:

```bash
.tools/bin/templ generate ./internal/web
```

Alternatively, all three version pins in `go.mod`, `go.sum`, and
`scripts/install-tools.sh` can be upgraded together. Do not upgrade only the
global executable or only the Go runtime.
