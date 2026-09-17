.PHONY: help build constituents lint run integration taxonomy-validation release test clean

.DEFAULT_GOAL := help

BIN_DIR := bin

help:
	@printf '%s\n' \
		'Available commands:' \
		'  make help         Display this command list' \
		'  make build        Build all command binaries' \
		'  make constituents Download the current S&P 500 constituent set' \
		'  make lint         Run golangci-lint with auto-fixes' \
		'  make integration  Run the EDGAR end-to-end test' \
		'  make taxonomy-validation  Run the 19-company taxonomy validation pass' \
		'  make run          Run the ECB command' \
		'  make serve        Serve locally parsed EDGAR filings' \
		'  make test         Run the Go test suite' \
		'  make clean        Remove build artifacts' \

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/edgar ./cmd/edgar
	go build -o $(BIN_DIR)/constituents ./cmd/constituents

constituents:
	mkdir -p data/sets
	go run ./cmd/constituents

lint:
	golangci-lint run --fix ./cmd/... ./internal/...

integration: build
	bash scripts/edgar-e2e.sh

taxonomy-validation: build
	bash scripts/taxonomy-validation.sh

run:
	go run ./cmd/ecb

serve:
	LOGLEVEL=debug ./bin/edgar serve --set us-gaap-coverage

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist
