.PHONY: help deps generate check-generated build run test lint docs-pdf clean \
	gateway-start gateway-stop gateway-status

.DEFAULT_GOAL := help

BIN_DIR := bin
SQLC ?= .tools/bin/sqlc
TEMPL ?= .tools/bin/templ
CHOP_GATEWAY_DIR ?= ../../wingmans/go-proxy-app/throttling
CHOP_GATEWAY_RULES := $(CURDIR)/infra/outbound.rules

help:
	@printf '%s\n' \
		'' \
		'Coding:' \
		'' \
		'  help                    Display this command list' \
		'  deps                    Prepare the development environment' \
		'  generate                Generate checked-in database code' \
		'  check-generated         Verify generated database code' \
		'  build                   Build all command binaries' \
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
		'' \

deps:
	@bash -c '[[ -f .env ]] && echo ".env already exists" || { \
		cp .env.example .env && echo "created .env"; \
	}'
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

check-generated: generate
	$(SQLC) vet
	@test -z "$$(git status --short --untracked-files=all -- \
		internal/edgar/pipeline/dbgen \
		internal/market/store/dbgen \
		internal/edgar/filing/store/dbgen \
		internal/edgar/news/dbgen \
		internal/edgar/mirror/dbgen)" || { \
		echo 'generated database code is out of date' >&2; \
		git status --short --untracked-files=all -- \
			internal/edgar/pipeline/dbgen \
			internal/market/store/dbgen \
			internal/edgar/filing/store/dbgen \
			internal/edgar/news/dbgen \
			internal/edgar/mirror/dbgen; \
		exit 1; \
	}

build: generate
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ingest/ecb
	go build -o $(BIN_DIR)/chop ./cmd/chop
	go build -o $(BIN_DIR)/chopd ./cmd/chopd
	go build -o $(BIN_DIR)/constituents ./cmd/ingest/constituents
	go build -o $(BIN_DIR)/yahoo-prices ./cmd/ingest/yahoo

run: generate
	$(MAKE) gateway-start
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
	@test -d "$(CHOP_GATEWAY_DIR)" || { \
		echo 'gateway repository not found: $(CHOP_GATEWAY_DIR)' >&2; exit 1; \
	}
	$(MAKE) -C "$(CHOP_GATEWAY_DIR)" up \
		RULES_FILE="$(CHOP_GATEWAY_RULES)"

gateway-stop:
	@test -d "$(CHOP_GATEWAY_DIR)" || { \
		echo 'gateway repository not found: $(CHOP_GATEWAY_DIR)' >&2; exit 1; \
	}
	$(MAKE) -C "$(CHOP_GATEWAY_DIR)" down

gateway-status:
	@test -d "$(CHOP_GATEWAY_DIR)" || { \
		echo 'gateway repository not found: $(CHOP_GATEWAY_DIR)' >&2; exit 1; \
	}
	$(MAKE) -C "$(CHOP_GATEWAY_DIR)" status
