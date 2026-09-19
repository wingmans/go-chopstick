.PHONY: help build constituents lint run integration taxonomy-validation taxonomy-validation-fast manual-checks release test clean clean-derived-data

.DEFAULT_GOAL := help

BIN_DIR := bin

help:
	@printf '%s\n' \
		'Available commands:' \
		'  make help         Display this command list' \
		'  make clean        Remove build artifacts' \
		'  make build        Build all command binaries' \
		'  make lint         Run golangci-lint with auto-fixes' \
		'  make test         Run the Go test suite' \
		'  make serve        Serve locally parsed EDGAR filings' \
		'  make constituents Download the current S&P 500 constituent set' \
		'  make clean-derived-data  Remove local EDGAR data while preserving raw filings' \
		'  make integration  Run the EDGAR end-to-end test' \
	  '  make taxonomy-validation  Run the 19-company taxonomy validation pass' \
	  '  make taxonomy-validation-fast  Run the five-company one-year validation pass' \
	  '  make manual-checks  Compare the confirmed baseline with local parsed 10-K views' \

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

taxonomy-validation-fast: build
	SET_NAME=golden FROM_YEAR=2024 TO_YEAR=2024 bash scripts/taxonomy-validation.sh

manual-checks:
	bash scripts/manual-checks.sh

clean-derived-data:
	bash scripts/clean-derived-data.sh

run:
	go run ./cmd/ecb

serve: build
	LOGLEVEL=debug ./bin/edgar serve --set us-gaap-coverage

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist
