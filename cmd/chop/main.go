// Command chop manages SEC EDGAR data and the local application.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"

	"wingman.com/chopstick/internal/choppie"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/envfile"
)

type leafCommand struct {
	Args []string `arg:"" optional:"" passthrough:""`
}

type cli struct {
	Backfill  leafCommand  `cmd:"" help:"Run a finite historical pipeline."     passthrough:""`
	Doctor    leafCommand  `cmd:"" help:"Validate local dependencies."          passthrough:""`
	Status    leafCommand  `cmd:"" help:"Show live application status."         passthrough:""`
	Filings   leafCommand  `cmd:"" help:"Download and process filings."         passthrough:""`
	Parse     leafCommand  `cmd:"" help:"Parse local submissions."              passthrough:""`
	Coverage  leafCommand  `cmd:"" help:"Report local filing coverage."         passthrough:""`
	Rebuild   rebuildCLI   `cmd:"" help:"Rebuild disposable state."`
	Index     indexCLI     `cmd:"" help:"Manage SEC indexes."`
	Ownership ownershipCLI `cmd:"" help:"Import and inspect ownership filings."`
	Failures  failuresCLI  `cmd:"" help:"Inspect and retry failures."`
	Artifacts artifactsCLI `cmd:"" help:"Inspect filing artifacts."`
	Taxonomy  taxonomyCLI  `cmd:"" help:"Inspect taxonomy coverage."`
}

type rebuildCLI struct {
	Edgar leafCommand `cmd:"" help:"Rebuild EDGAR state." passthrough:""`
}

type indexCLI struct {
	Download leafCommand `cmd:"" default:"withargs"           help:"Download SEC indexes." passthrough:""`
	Import   leafCommand `cmd:"" help:"Import local indexes." passthrough:""`
}

type ownershipCLI struct {
	Import leafCommand `cmd:"" default:"withargs"            help:"Import ownership filings." passthrough:""`
	Status leafCommand `cmd:"" help:"Show ownership status." passthrough:""`
}

type failuresCLI struct {
	List  leafCommand `cmd:"" help:"List unresolved failures."  passthrough:""`
	Retry leafCommand `cmd:"" help:"Retry unresolved failures." passthrough:""`
}

type artifactsCLI struct {
	List    leafCommand `cmd:"" help:"List source documents."     passthrough:""`
	Extract leafCommand `cmd:"" help:"Extract source documents."  passthrough:""`
	Serve   leafCommand `cmd:"" help:"Serve extracted documents." passthrough:""`
	Clean   leafCommand `cmd:"" help:"Clean extracted documents." passthrough:""`
}

type taxonomyCLI struct {
	Lint     leafCommand `cmd:"" help:"Lint compact filing views." passthrough:""`
	Coverage leafCommand `cmd:"" help:"Report taxonomy coverage."  passthrough:""`
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	if err := envfile.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var model cli

	parser, err := kong.New(&model,
		kong.Name("chop"),
		kong.Description("Operate the local Chop application and SEC EDGAR data."),
		kong.Writers(os.Stdout, os.Stderr),
	)
	if err != nil {
		logger.Error("build CLI", "error", err)

		return 1
	}

	parsed, err := parser.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	args := selectedArgs(parsed.Command(), &model)
	if err := choppie.Execute(ctx, args); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown requested")

			return 0
		}

		logger.Error("command failed", "error", err)

		return 1
	}

	return 0
}

func selectedArgs(command string, model *cli) []string {
	switch {
	case strings.HasPrefix(command, "backfill"):
		return append([]string{"backfill"}, model.Backfill.Args...)
	case strings.HasPrefix(command, "doctor"):
		return append([]string{"doctor"}, model.Doctor.Args...)
	case strings.HasPrefix(command, "status"):
		return append([]string{"status"}, model.Status.Args...)
	case strings.HasPrefix(command, "filings"):
		return append([]string{"filings"}, model.Filings.Args...)
	case strings.HasPrefix(command, "parse"):
		return append([]string{"parse"}, model.Parse.Args...)
	case strings.HasPrefix(command, "coverage"):
		return append([]string{"coverage"}, model.Coverage.Args...)
	case strings.HasPrefix(command, "rebuild edgar"):
		return append([]string{"rebuild", "edgar"}, model.Rebuild.Edgar.Args...)
	case strings.HasPrefix(command, "index import"):
		return append([]string{"index", "import"}, model.Index.Import.Args...)
	case strings.HasPrefix(command, "index"):
		return append([]string{"index"}, model.Index.Download.Args...)
	case strings.HasPrefix(command, "ownership status"):
		return append([]string{"ownership", "status"}, model.Ownership.Status.Args...)
	case strings.HasPrefix(command, "ownership"):
		return append([]string{"ownership", "import"}, model.Ownership.Import.Args...)
	case strings.HasPrefix(command, "failures list"):
		return append([]string{"failures", "list"}, model.Failures.List.Args...)
	case strings.HasPrefix(command, "failures retry"):
		return append([]string{"failures", "retry"}, model.Failures.Retry.Args...)
	case strings.HasPrefix(command, "artifacts list"):
		return append([]string{"artifacts", "list"}, model.Artifacts.List.Args...)
	case strings.HasPrefix(command, "artifacts extract"):
		return append([]string{"artifacts", "extract"}, model.Artifacts.Extract.Args...)
	case strings.HasPrefix(command, "artifacts serve"):
		return append([]string{"artifacts", "serve"}, model.Artifacts.Serve.Args...)
	case strings.HasPrefix(command, "artifacts clean"):
		return append([]string{"artifacts", "clean"}, model.Artifacts.Clean.Args...)
	case strings.HasPrefix(command, "taxonomy lint"):
		return append([]string{"taxonomy", "lint"}, model.Taxonomy.Lint.Args...)
	case strings.HasPrefix(command, "taxonomy coverage"):
		return append([]string{"taxonomy", "coverage"}, model.Taxonomy.Coverage.Args...)
	default:
		return nil
	}
}
