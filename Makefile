.PHONY: help setup build test lint docs-pdf clean

.DEFAULT_GOAL := help

BIN_DIR := bin
CHOPSTICK_DATA_DIR ?= ./data

-include .env

CHOPSTICK_DATA_DIR := $(or $(strip $(CHOPSTICK_DATA_DIR)),./data)

export EDGAR_SET
export EDGAR_FORM_TYPES
export EDGAR_FROM_YEAR
export EDGAR_NATS_URL
export EDGAR_NEWS_ADDRESS
export EDGAR_FILINGS_ADDRESS
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

include make/development.mk

help:
	@printf '%s\n' \
		'' \
		'Development:' \
		'' \
		'  help                    Display this command list' \
		'  setup                   Prepare the development environment' \
		'  dev                     Run all services; requires NATS' \
		'  filings-web             Run only the filings web server' \
		'  build                   Build all command binaries' \
		'  test                    Run the Go test suite' \
		'  lint                    Run golangci-lint with auto-fixes' \
		'  docs-pdf                Build the documentation PDF' \
		'  clean                   Remove build artifacts' \
		'' \
		'Local data:' \
		'' \
		'  clean-dev               Reset processing but preserve downloads' \
		'  reset-dev               Remove all local application data' \
		'' \
		'Infrastructure:' \
		'' \
		'  nats-start              Start project-local NATS' \
		'  nats-stop               Stop project-local NATS' \
		'  nats-status             Show project-local NATS status' \
		'  nats-reset              Remove local NATS state' \
		'' \
		'Operations:' \
		'' \
		'  doctor                  Validate pipeline dependencies' \
		'  doctor-verbose          Validate and show configuration' \
		'  status                  Show pipeline and NATS state' \
		'  errors                  Show unresolved pipeline failures' \
		'  retry-download          Requeue failed downloads' \
		'  retry-process           Requeue failed processing work' \
		'' \
		'Observability:' \
		'' \
		'  logs                    Follow all application logs' \
		'  logs-atom               Follow Atom discovery logs' \
		'  logs-download           Follow download worker logs' \
		'  logs-process            Follow processing worker logs' \
		'  logs-news               Follow news service logs' \
		'  logs-filings            Follow filings web logs' \
		'  logs-errors             Follow warnings and errors' \
		'' \
		'More commands: go run ./cmd/edgar --help'

setup:
	@bash -c '[[ -f .env ]] && echo ".env already exists" || { \
		cp .env.example .env && echo "created .env"; \
	}'
	bash scripts/install-tools.sh

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/edgar ./cmd/edgar
	go build -o $(BIN_DIR)/constituents ./cmd/constituents
	go build -o $(BIN_DIR)/polling ./cmd/polling
	go build -o $(BIN_DIR)/polling-reader ./cmd/polling-reader

lint:
	golangci-lint run --fix ./cmd/... ./internal/...

docs-pdf:
	bash scripts/docs-pdf.sh

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist
