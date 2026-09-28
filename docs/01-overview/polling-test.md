# Polling test

Build and run the polling worker with debug logging:

```sh
go build -o bin/polling ./cmd/polling
LOGLEVEL=DEBUG ./bin/polling \
  -interval=1h \
  -db-path=./data/polling.db \
  -user-agent="Paul paul@wingmen.io" \
  >> ./data/polling.log 2>&1
```

Inspect the latest database activity:

```sh
sqlite3 -header -column ./data/polling.db \
  'SELECT COUNT(*) AS records, MAX(processed_at) AS last_processed
   FROM processed_accessions;'
```

Check for missing metadata:

```sh
sqlite3 ./data/polling.db \
  'SELECT COUNT(*) FROM processed_accessions
   WHERE cik = "" OR form_type = "";'
```

Check quarantined entries:

```sh
sqlite3 -header -column ./data/polling.db \
  'SELECT received_at, title, reason FROM polling_dead_letters
   ORDER BY received_at DESC LIMIT 20;'
```

The expected results are advancing `last_processed`, no missing metadata, and
only understandable entries in the dead-letter table.
