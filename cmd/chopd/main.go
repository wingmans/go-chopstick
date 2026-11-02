// Command chopd runs the Chop application as one foreground process.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"wingman.com/chopstick/internal/buildinfo"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgarapp"
	"wingman.com/chopstick/internal/envfile"
)

type daemonCLI struct {
	Version kong.VersionFlag `group:"diag" help:"Print version."                              name:"version" short:"v"`
	Verbose bool             `group:"diag" help:"Print verbose output and use debug logging."`

	Log *string `enum:"critical,error,warn,info,debug,trace,off" group:"diag" help:"Log level (default: info)." placeholder:"level"`

	Address        string `default:"${http_address}"    group:"network" help:"HTTP listen address."        placeholder:"host:port"`
	CommandAddress string `default:"${command_address}" group:"network" help:"Local chop command address." placeholder:"host:port"`
	NATSURL        string `default:"${nats_url}"        group:"network" help:"NATS event-bus URL."         name:"nats-url"         placeholder:"url"`

	PollingDB   string `default:"${polling_db}"   group:"storage" help:"Pipeline state database."     placeholder:"path"`
	NewsDB      string `default:"${news_db}"      group:"storage" help:"News projection database."    placeholder:"path"`
	AnalysisDB  string `default:"${analysis_db}"  group:"storage" help:"Analysis database."           placeholder:"path"`
	OwnershipDB string `default:"${ownership_db}" group:"storage" help:"Ownership database."          placeholder:"path"`
	FilingsDir  string `default:"${filings_dir}"  group:"storage" help:"Downloaded filing directory." placeholder:"path"`
	ParsedDir   string `default:"${parsed_dir}"   group:"storage" help:"Parsed filing directory."     placeholder:"path"`

	UserAgent string `default:"${user_agent}" group:"edgar" help:"SEC request user agent."         placeholder:"value"`
	BaseURL   string `default:"${base_url}"   group:"edgar" help:"SEC Archives base URL override." placeholder:"url"`

	Interval    time.Duration `default:"${interval}"     group:"runtime" help:"SEC Atom polling interval."             placeholder:"duration"`
	MaxAttempts int           `default:"${max_attempts}" group:"runtime" help:"Attempts before work is marked failed." placeholder:"count"`
}

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	if err := envfile.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	config, err := edgarapp.ConfigFromEnvironment()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	var cli daemonCLI

	parser, err := kong.New(&cli,
		kong.Name("chopd"),
		kong.Description(
			"Run the local Chop ingestion, processing, and web application.",
		),
		kong.Writers(os.Stdout, os.Stderr),
		kong.Vars{
			"version":         buildinfo.Version,
			"http_address":    config.HTTPAddress,
			"command_address": config.CommandAddress,
			"nats_url":        config.NATSURL,
			"polling_db":      config.PollingDB,
			"news_db":         config.NewsDB,
			"analysis_db":     config.AnalysisDB,
			"ownership_db":    config.OwnershipDB,
			"filings_dir":     config.FilingsDirectory,
			"parsed_dir":      config.ParsedDirectory,
			"user_agent":      config.UserAgent,
			"base_url":        config.BaseURL,
			"interval":        config.Interval.String(),
			"max_attempts":    strconv.Itoa(config.MaxAttempts),
		},
		kong.Groups{
			"network": "Network",
			"storage": "Storage",
			"edgar":   "SEC EDGAR",
			"runtime": "Runtime",
			"diag":    "Troubleshooting",
		},
		kong.ConfigureHelp(kong.HelpOptions{
			Compact:        true,
			FlagsLast:      true,
			Summary:        true,
			WrapUpperBound: 100,
		}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	if _, err := parser.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	if cli.Verbose {
		level := "debug"
		cli.Log = &level
	}

	if cli.Log != nil {
		if err := os.Setenv("LOG_LEVEL", *cli.Log); err != nil {
			fmt.Fprintln(os.Stderr, err)

			return 1
		}
	}

	applyCLI(&config, cli)

	if err := config.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	if err := run(config); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return 0
}

func applyCLI(config *edgarapp.Config, cli daemonCLI) {
	config.HTTPAddress = cli.Address
	config.CommandAddress = cli.CommandAddress
	config.NATSURL = cli.NATSURL
	config.PollingDB = cli.PollingDB
	config.NewsDB = cli.NewsDB
	config.AnalysisDB = cli.AnalysisDB
	config.OwnershipDB = cli.OwnershipDB
	config.FilingsDirectory = cli.FilingsDir
	config.ParsedDirectory = cli.ParsedDir
	config.UserAgent = cli.UserAgent
	config.BaseURL = cli.BaseURL
	config.Interval = cli.Interval
	config.MaxAttempts = cli.MaxAttempts
}

func run(config edgarapp.Config) error {
	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting Chop application",
		"address", config.HTTPAddress,
		"command_address", config.CommandAddress,
		"nats_url", config.NATSURL,
		"set", config.SetName,
		"form_types", config.FormTypes,
	)

	if err := edgarapp.Run(ctx, config, logger); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		return fmt.Errorf("run Chop application: %w", err)
	}

	return nil
}
