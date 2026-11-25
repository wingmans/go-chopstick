.PHONY: help deps generate build setup run test lint \
	docs-pdf clean nats-start nats-stop nats-status nats-reset \
	gateway-start gateway-stop gateway-status gateway-reset

.DEFAULT_GOAL := help

BIN_DIR := bin
SQLC ?= .tools/bin/sqlc
TEMPL ?= .tools/bin/templ

help:
	@printf '%s\n' \
		'' \
		'Coding:' \
		'' \
		'  help                    Display this command list' \
		'  deps                    Prepare the development environment' \
		'  generate                Generate checked-in database code' \
		'  build                   Build all command binaries' \
		'  setup                   Start local dependencies and initialize Chopstick' \
		'  run                     Run chopd in the foreground' \
		'  test                    Run the Go test suite' \
		'  lint                    Run golangci-lint with auto-fixes' \
		'  docs-pdf                Build the documentation PDF' \
		'  clean                   Remove build artifacts' \
		'' \
		'Infrastructure:' \
		'' \
		'  nats-start              Start project-local NATS' \
		'  nats-stop               Stop project-local NATS' \
		'  nats-status             Show project-local NATS status' \
		'  nats-reset              Remove local NATS state' \
		'  gateway-start           Start the outbound HTTP gateway' \
		'  gateway-stop            Stop the outbound HTTP gateway' \
		'  gateway-status          Show outbound gateway status' \
		'  gateway-reset           Rebuild and rotate gateway credentials' \
		'' \

deps:
	@test -f .env && { echo '.env already exists'; exit $$?; }; \
		cp .env.example .env && echo 'created .env'
	bash scripts/install-tools.sh

generate:
	@command -v "$(TEMPL)" >/dev/null 2>&1 || { \
		echo 'templ is unavailable; run make deps' >&2; exit 1; \
	}
	@command -v "$(SQLC)" >/dev/null 2>&1 || { \
		echo 'sqlc is unavailable; run make deps' >&2; exit 1; \
	}
	$(TEMPL) generate ./internal/chopstick/web
	$(SQLC) generate

build: generate
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ingest/ecb
	go build -o $(BIN_DIR)/chop ./cmd/chop
	go build -o $(BIN_DIR)/chopd ./cmd/chopd
	go build -o $(BIN_DIR)/constituents ./cmd/ingest/constituents
	go build -o $(BIN_DIR)/yahoo-prices ./cmd/ingest/yahoo

setup: build
	$(MAKE) nats-start
	./$(BIN_DIR)/chop setup
	@$(MAKE) gateway-start || printf '%s\n' \
		'warning: Viewers can run. For downloads, retry with: make gateway-start'

run: generate
	go run ./cmd/chopd

lint: generate
	golangci-lint run --fix ./cmd/... ./internal/...

docs-pdf:
	bash scripts/docs-pdf.sh

test: generate
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist
	find internal/chopstick/web -type f -name '*_templ.go' -delete

nats-start:
	bash scripts/nats-dev.sh start

nats-stop:
	bash scripts/nats-dev.sh stop

nats-status:
	bash scripts/nats-dev.sh status

nats-reset:
	bash scripts/nats-dev.sh reset

gateway-start:
	bash scripts/gateway-dev.sh start

gateway-stop:
	bash scripts/gateway-dev.sh stop

gateway-status:
	bash scripts/gateway-dev.sh status

gateway-reset:
	bash scripts/gateway-dev.sh reset
