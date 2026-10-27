.PHONY: dev market-dev rebuild-edgar rebuild-market watch overview \
	index-refresh prepare-dev-data clean-dev reset-dev nats-start nats-stop \
	nats-status nats-reset \
	doctor doctor-verbose status errors retry-download retry-process logs \
	logs-atom logs-select logs-discovery logs-download logs-process logs-news \
	logs-filings logs-errors \
	filings-web

MARKET_MANIFEST ?= $(CHOPSTICK_DATA_DIR)/sets/sp500.json
MARKET_FROM ?= 2026-01-01

dev: nats-start prepare-dev-data
	mkdir -p "$(CHOPSTICK_DATA_DIR)/logs"
	EDGAR_LOG="$(CHOPSTICK_DATA_DIR)/logs/edgar.log" \
		bash -o pipefail -c \
		'go run ./cmd/edgar run 2>&1 | tee -a "$${EDGAR_LOG}"'

market-dev:
	go run ./cmd/yf serve \
		--manifest "$(MARKET_MANIFEST)" \
		--from "$(MARKET_FROM)"

rebuild-edgar:
	$(MAKE) clean-dev
	$(MAKE) prepare-dev-data
	$(MAKE) nats-start
	go run ./cmd/edgar backfill
	@printf '%s\n' \
		'EDGAR state rebuilt; make dev will replay the news projection'

rebuild-market:
	bash scripts/clean-market-db.sh
	go run ./cmd/yf sync \
		--manifest "$(MARKET_MANIFEST)" \
		--from "$(MARKET_FROM)" \
		--max-symbols 0

watch: index-refresh
	/usr/bin/osascript scripts/watch.applescript "$(CURDIR)"

overview:
	bash scripts/pipeline-overview.sh

index-refresh:
	go run ./cmd/edgar index --refresh-latest

prepare-dev-data:
	mkdir -p "$(CHOPSTICK_DATA_DIR)/sets"
	test -f "$(CHOPSTICK_DATA_DIR)/sets/us-gaap.json" || \
		cp golden/us-gaap.json "$(CHOPSTICK_DATA_DIR)/sets/us-gaap.json"

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

logs-select:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*select'

logs-discovery:
	tail -F "$(CHOPSTICK_DATA_DIR)/logs/edgar.log" | \
		rg --line-buffered 'role=.*(atom|select)'

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
