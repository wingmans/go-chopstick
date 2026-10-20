.PHONY: nats-dev nats-start nats-stop nats-status nats-reset doctor \
	doctor-verbose status errors retry-download retry-process index discover \
	backfill dev logs logs-errors logs-download logs-process constituents run \
	serve serve-dev build-polling run-polling test-polling

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
	mkdir -p "$(CHOPSTICK_DATA_DIR)/logs"
	EDGAR_LOG="$(CHOPSTICK_DATA_DIR)/logs/edgar.log" \
		bash -o pipefail -c \
		'go run ./cmd/edgar backfill 2>&1 | tee -a "$${EDGAR_LOG}"'

dev: nats-start
	mkdir -p "$(CHOPSTICK_DATA_DIR)/logs"
	EDGAR_LOG="$(CHOPSTICK_DATA_DIR)/logs/edgar.log" \
		bash -o pipefail -c \
		'go run ./cmd/edgar run 2>&1 | tee -a "$${EDGAR_LOG}"'

logs:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log"

logs-errors:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'WRN|ERR'

logs-download:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=download'

logs-process:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=process'

constituents:
	mkdir -p "$(CHOPSTICK_DATA_DIR)/sets"
	go run ./cmd/constituents

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

build-polling:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/polling ./cmd/polling
	go build -o $(BIN_DIR)/polling-reader ./cmd/polling-reader

run-polling:
	mkdir -p "$(CHOPSTICK_DATA_DIR)"
	go run ./cmd/polling \
		-interval=1h \
		-user-agent="MyMarketPipeline contact@example.com"

test-polling:
	go test ./cmd/polling ./internal/pollingworker
