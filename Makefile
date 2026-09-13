.PHONY: build lint run integration release test clean

BIN_DIR := bin

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ecb-fx ./cmd/ecb
	go build -o $(BIN_DIR)/edgar ./cmd/edgar

lint:
	golangci-lint run ./cmd/... ./internal/...

integration: build
	bash scripts/edgar-e2e.sh

run:
	go run ./cmd/ecb

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR) dist

release:
	goreleaser release --clean
