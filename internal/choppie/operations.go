package choppie

import (
	"errors"
	"flag"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/transport"
)

// operationsConfig holds the configuration for the operations subcommands.
// It includes settings for NATS, databases, file paths, HTTP addresses, and command-specific options.
type operationsConfig struct {
	natsURL        string
	commandAddress string
	pollingDB      string
	newsDB         string
	masterPath     string
	httpAddress    string
	stage          string
	limit          int
	verbose        bool
}

// parseOperationsConfig parses the command-line arguments and environment variables
// for the operations subcommands, returning an appConfig with the parsed settings.
func parseOperationsConfig(command string, args []string) (appConfig, error) {
	displayCommand := command
	switch command {
	case "errors":
		displayCommand = "failures list"
	case "retry":
		displayCommand = "failures retry"
	}

	cfg := appConfig{
		command: command,
		operations: operationsConfig{
			natsURL: environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
			commandAddress: environmentValue(
				chopCommandAddrEnv,
				transport.DefaultAddress,
			),
			pollingDB:   appdata.Path("polling.db"),
			newsDB:      appdata.Path("feed.db"),
			masterPath:  appdata.Path("cache", "indexes", "master.tsv"),
			httpAddress: webAddressFromEnvironment(),
			limit:       50,
		},
	}

	flags := flag.NewFlagSet(displayCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)

	options := []helpOption{}

	switch command {
	case "doctor":
		flags.StringVar(&cfg.operations.natsURL, "nats-url", cfg.operations.natsURL, "NATS server URL")
		flags.StringVar(&cfg.operations.pollingDB, "polling-db", cfg.operations.pollingDB, "pipeline state database")
		flags.StringVar(&cfg.operations.newsDB, "news-db", cfg.operations.newsDB, "disposable news database")
		flags.StringVar(&cfg.operations.masterPath, "master", cfg.operations.masterPath, "stitched master index")
		flags.BoolVar(&cfg.operations.verbose, "verbose", false, "show resolved configuration and details")

		options = append(options,
			helpOption{"--nats-url <url>", "NATS server URL."},
			helpOption{"--polling-db <path>", "Pipeline state database."},
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--master <path>", "Stitched master index."},
			helpOption{"--verbose", "Show configuration and check details."},
		)
	case "status", "errors", "retry":
		flags.StringVar(&cfg.operations.commandAddress, "command-address",
			cfg.operations.commandAddress, "chopd command address")

		options = append(options, helpOption{
			"--command-address <host:port>", "chopd command address.",
		})
	}

	switch command {
	case "errors":
		flags.StringVar(&cfg.operations.stage, "stage", "", "limit failures to download or process")
		flags.IntVar(&cfg.operations.limit, "limit", cfg.operations.limit, "maximum failures to show")

		options = append(options,
			helpOption{"--stage <stage>", "Limit failures to download or process."},
			helpOption{"--limit <count>", "Maximum failures to show; default 50."},
		)
	case "retry":
		flags.StringVar(&cfg.operations.stage, "stage", "", "failure stage to retry")

		options = append(options,
			helpOption{"--stage <stage>", "Required: download or process."},
		)
	}

	flags.Usage = func() {
		printSubcommandHelp(displayCommand, operationsDescription(command),
			options)
	}
	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.operations.limit <= 0 {
		return subcommandError(flags, errors.New("--limit must be positive"))
	}

	if cfg.operations.stage != "" && cfg.operations.stage != "download" &&
		cfg.operations.stage != "process" {
		return subcommandError(flags,
			errors.New("--stage must be download or process"))
	}

	if command == "retry" && cfg.operations.stage == "" {
		return subcommandError(flags, errors.New("--stage is required"))
	}

	return cfg, nil
}

func operationsDescription(command string) string {
	return map[string]string{
		"doctor": "Validate configuration and dependencies without changing them.",
		"status": "Show current outbox, stream, consumer, failure, and viewer state.",
		"errors": "List unresolved download and processing failures.",
		"retry":  "Requeue unresolved failures for one stage.",
	}[command]
}

func configuredFormTypes() ([]string, error) {
	value := environmentValue(edgarFormTypesEnv, "10-K,10-Q,8-K")

	return edgar.ValidateSupportedFormTypes(strings.Split(value, ","))
}
