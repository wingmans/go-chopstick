.PHONY: help setup generate check-generated build run test lint docs-pdf clean

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
		'  deps                   Prepare the development environment' \
		'  generate                Generate checked-in database code' \
		'  check-generated          Verify generated database code' \
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
		'' \

deps:
	@bash -c '[[ -f .env ]] && echo ".env already exists" || { \
		cp .env.example .env && echo "created .env"; \
	}'
	bash scripts/install-tools.sh

generate:
	@command -v "$(TEMPL)" >/dev/null 2>&1 || { \
		echo 'templ is unavailable; run make setup' >&2; exit 1; \
	}
	@command -v "$(SQLC)" >/dev/null 2>&1 || { \
		echo 'sqlc is unavailable; run make setup' >&2; exit 1; \
	}
	$(TEMPL) generate ./internal/web
	$(SQLC) generate

check-generated: generate
	$(SQLC) vet
	@test -z "$$(git status --short --untracked-files=all -- \
		internal/chopstick/pipeline/dbgen \
		internal/market/store/dbgen \
		internal/filing/store/dbgen \
		internal/news/dbgen)" || { \
		echo 'generated database code is out of date' >&2; \
		git status --short --untracked-files=all -- \
			internal/chopstick/pipeline/dbgen \
			internal/market/store/dbgen \
			internal/filing/store/dbgen \
			internal/news/dbgen; \
		exit 1; \
	}

build: generate
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/chop ./cmd/chop
	go build -o $(BIN_DIR)/chopd ./cmd/chopd
	go build -o $(BIN_DIR)/constituents ./cmd/constituents
	go build -o $(BIN_DIR)/yahoo-prices ./cmd/yf

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
	find internal/web -type f -name '*_templ.go' -delete

nats-start:
	bash scripts/nats-dev.sh start

nats-stop:
	bash scripts/nats-dev.sh stop

nats-status:
	bash scripts/nats-dev.sh status

nats-reset:
	bash scripts/nats-dev.sh reset
