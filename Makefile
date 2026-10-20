.PHONY: help env deps build lint integration taxonomy-validation \
	taxonomy-validation-fast manual-checks docs-pdf test clean clean-dev \
	reset-dev

.DEFAULT_GOAL := help

BIN_DIR := bin
CHOPSTICK_DATA_DIR ?= ./data

-include .env

CHOPSTICK_DATA_DIR := $(or $(strip $(CHOPSTICK_DATA_DIR)),./data)

export EDGAR_SET
export EDGAR_FROM_YEAR
export EDGAR_NATS_URL
export EDGAR_USER_AGENT
export EDGAR_BASE_URL
export CHOPSTICK_DATA_DIR
export LOGLEVEL

ifneq ($(strip $(SET)),)
EDGAR_SET := $(SET)
endif

ifneq ($(strip $(FROM_YEAR)),)
EDGAR_FROM_YEAR := $(FROM_YEAR)
endif

include make/operations.mk

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
		'  clean-dev               Reset processing but preserve downloads' \
		'  reset-dev               Remove all local application data' \
		'' \
		'  constituents            Download the current S&P 500 constituent set' \
		'  integration             Run the EDGAR end-to-end test' \
		'  taxonomy-validation     Run the 19-company taxonomy validation pass' \
		'  taxonomy-validation-fast Run the five-company one-year validation pass' \
		'  manual-checks           Compare the confirmed baseline with parsed 10-K views' \
		'  run                     Fetch ECB data' \
		'  serve                   Serve locally parsed EDGAR filings' \
		'  serve-dev               Serve filings with Air auto-rebuilds' \

deps:
	bash scripts/install-tools.sh

env:
	@bash -c '[[ -f .env ]] && echo ".env already exists" || { cp .env.example .env && echo "created .env"; }'

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/edgar ./cmd/edgar
	go build -o $(BIN_DIR)/constituents ./cmd/constituents
	go build -o $(BIN_DIR)/polling ./cmd/polling
	go build -o $(BIN_DIR)/polling-reader ./cmd/polling-reader

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

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist

clean-dev: nats-stop
	bash scripts/clean-dev.sh

reset-dev: nats-stop
	bash scripts/reset-dev.sh
