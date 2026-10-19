.PHONY: help env deps nats-dev nats-start nats-stop nats-status nats-reset doctor doctor-verbose status errors retry-download retry-process index discover backfill dev logs logs-errors logs-download logs-process build constituents lint run serve serve-dev integration taxonomy-validation taxonomy-validation-fast manual-checks docs-pdf release test clean clean-analysis-data build-polling run-polling test-polling

.DEFAULT_GOAL := help

BIN_DIR := bin

-include .env

export EDGAR_SET
export EDGAR_FROM_YEAR
export EDGAR_NATS_URL
export EDGAR_USER_AGENT
export EDGAR_BASE_URL
export LOGLEVEL

ifneq ($(strip $(SET)),)
EDGAR_SET := $(SET)
endif

ifneq ($(strip $(FROM_YEAR)),)
EDGAR_FROM_YEAR := $(FROM_YEAR)
endif

help:
	@printf '%s\n' \
		'' \
		'Available commands:' \
		'' \
		'  help                    Display this command list' \
		'  env                     Create .env from the example if missing' \
		'  deps                    Install project-local development tools' \
		'  nats-dev                Alias for nats-start' \
		'  nats-start              Start the project-local NATS server' \
		'  nats-stop               Stop the project-local NATS server' \
		'  nats-status             Show the project-local NATS server status' \
		'  nats-reset              Remove local NATS JetStream data' \
		'  doctor                  Validate pipeline dependencies' \
		'  doctor-verbose          Validate and show resolved configuration' \
		'  status                  Show pipeline state and NATS backlogs' \
		'  errors                  Show unresolved pipeline failures' \
		'  retry-download          Requeue failed downloads' \
		'  retry-process           Requeue failed processing work' \
		'  index                   Download and stitch SEC indexes' \
		'  discover                Publish the configured filing scope' \
		'  backfill                Run index through processing' \
		'  dev                     Run the modular monolith in the foreground' \
		'  logs                    Follow the development log' \
		'  logs-errors             Follow warnings and errors' \
		'  logs-download           Follow download-role messages' \
		'  logs-process            Follow process-role messages' \
		'  build                   Build all command binaries' \
		'  docs-pdf                Build one PDF from the documentation chapters' \
		'  lint                    Run golangci-lint with auto-fixes' \
		'  test                    Run the Go test suite' \
		'  clean                   Remove build artifacts' \
		'' \
		'  constituents            Download the current S&P 500 constituent set' \
		'  clean-analysis-data     Remove analysis data while preserving raw filings' \
		'  integration             Run the EDGAR end-to-end test' \
		'  taxonomy-validation     Run the 19-company taxonomy validation pass' \
		'  taxonomy-validation-fast Run the five-company one-year validation pass' \
		'  manual-checks           Compare the confirmed baseline with parsed 10-K views' \
		'  run                     Run the ECB example command' \
		'  serve                   Serve locally parsed EDGAR filings' \
		'  serve-dev               Serve filings with Air auto-rebuilds' \

deps:
	bash scripts/install-tools.sh

env:
	@bash -c '[[ -f .env ]] && echo ".env already exists" || { cp .env.example .env && echo "created .env"; }'

nats-dev: nats-start

nats-start:
	bash scripts/nats-dev.sh start

nats-stop:
	bash scripts/nats-dev.sh stop

nats-status:
	bash scripts/nats-dev.sh status

nats-reset:
	bash scripts/nats-dev.sh reset

doctor:
	go run ./cmd/edgar doctor

doctor-verbose:
	go run ./cmd/edgar doctor --verbose

status:
	go run ./cmd/edgar status

errors:
	go run ./cmd/edgar errors

retry-download:
	go run ./cmd/edgar retry --stage download

retry-process:
	go run ./cmd/edgar retry --stage process

index:
	go run ./cmd/edgar index

discover:
	go run ./cmd/edgar discover

backfill:
	mkdir -p data/logs
	bash -o pipefail -c 'go run ./cmd/edgar backfill 2>&1 | tee -a data/logs/edgar.log'

dev: nats-start
	mkdir -p data/logs
	bash -o pipefail -c 'go run ./cmd/edgar run 2>&1 | tee -a data/logs/edgar.log'

logs:
	tail -F data/logs/edgar.log

logs-errors:
	tail -F data/logs/edgar.log | rg --line-buffered 'WRN|ERR'

logs-download:
	tail -F data/logs/edgar.log | rg --line-buffered 'role=download'

logs-process:
	tail -F data/logs/edgar.log | rg --line-buffered 'role=process'

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/edgar ./cmd/edgar
	go build -o $(BIN_DIR)/constituents ./cmd/constituents
	go build -o $(BIN_DIR)/polling ./cmd/polling
	go build -o $(BIN_DIR)/polling-reader ./cmd/polling-reader

constituents:
	mkdir -p data/sets
	go run ./cmd/constituents

lint:
	golangci-lint run --fix ./cmd/... ./internal/...

integration: build
	bash scripts/edgar-e2e.sh

taxonomy-validation: build
	bash scripts/taxonomy-validation.sh

taxonomy-validation-fast: build
	SET_NAME=golden FROM_YEAR=2024 TO_YEAR=2024 bash scripts/taxonomy-validation.sh

manual-checks:
	bash scripts/manual-checks.sh

docs-pdf:
	bash scripts/docs-pdf.sh

clean-analysis-data:
	bash scripts/clean-analysis-data.sh

run:
	go run ./cmd/ecb

serve: build
	LOGLEVEL=debug ./bin/edgar serve --set us-gaap

serve-dev:
	@command -v air >/dev/null || { \
		printf '%s\n' 'air is not installed. Install it with:'; \
		printf '%s\n' '  go install github.com/air-verse/air@latest'; \
		exit 1; \
	}
	air -c .air.toml

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist tmp

build-polling:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/polling ./cmd/polling
	go build -o $(BIN_DIR)/polling-reader ./cmd/polling-reader

run-polling:
	mkdir -p var
	go run ./cmd/polling \
		-interval=1h \
		-db-path=./var/polling.db \
		-user-agent="MyMarketPipeline contact@example.com"

test-polling:
	go test ./cmd/polling ./internal/pollingworker
