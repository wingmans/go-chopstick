.PHONY: dev clean-dev reset-dev nats-start nats-stop nats-status nats-reset \
	doctor doctor-verbose status errors retry-download retry-process logs \
	logs-atom logs-download logs-process logs-news logs-filings logs-errors \
	filings-web

dev:
	mkdir -p "$(CHOPSTICK_DATA_DIR)/logs"
	EDGAR_LOG="$(CHOPSTICK_DATA_DIR)/logs/edgar.log" \
		bash -o pipefail -c \
		'go run ./cmd/edgar run 2>&1 | tee -a "$${EDGAR_LOG}"'

filings-web:
	go run ./cmd/edgar serve

clean-dev: nats-stop
	bash scripts/clean-dev.sh

reset-dev: nats-stop
	bash scripts/reset-dev.sh

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

logs:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log"

logs-atom:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*atom'

logs-download:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*download'

logs-process:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*process'

logs-news:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*news'

logs-filings:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*filings-web'

logs-errors:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'WRN|ERR'
