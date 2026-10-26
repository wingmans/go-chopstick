package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const pricesDirectory = "prices"

var (
	cikPattern    = regexp.MustCompile(`^[0-9]{10}$`)
	tickerPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]*$`)
	yearPattern   = regexp.MustCompile(`^[0-9]{4}\.csv$`)
)

type member struct {
	CIK    string `json:"cik"`
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
}

type constituentManifest struct {
	Name    string   `json:"name"`
	AsOf    string   `json:"as_of"`
	Source  string   `json:"source"`
	Members []member `json:"members"`
}

type candidateRow struct {
	CIK      string
	Ticker   string
	Name     string
	Date     time.Time
	Open     float64
	Close    float64
	AdjClose float64
}

type observationKey struct {
	ticker string
	date   string
}

type downloadResult struct {
	observations int
	partitions   int
}

type priceReader interface {
	LatestDates(ctx context.Context) (map[string]time.Time, error)
}

type priceRows func(yield func(candidateRow, error) bool)

type priceWriter interface {
	UpsertPrices(ctx context.Context, rows priceRows) (int, error)
}

type csvPriceStore struct {
	directory string
}

type priceSpool struct {
	file   *os.File
	writer *csv.Writer
}

func newCSVPriceStore(root string) (*csvPriceStore, error) {
	if root == "" || root == "." || root == string(filepath.Separator) {
		return nil, fmt.Errorf("unsafe storage directory %q", root)
	}

	directory := filepath.Join(root, pricesDirectory)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create prices directory: %w", err)
	}

	return &csvPriceStore{directory: directory}, nil
}

func (store *csvPriceStore) LatestDates(
	ctx context.Context,
) (map[string]time.Time, error) {
	return loadLatestDates(ctx, store.directory)
}

func (store *csvPriceStore) UpsertPrices(ctx context.Context, rows priceRows) (int, error) {
	stagingDirectory, err := os.MkdirTemp(store.directory, ".incoming-*")
	if err != nil {
		return 0, fmt.Errorf("create price staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDirectory) }()

	spools := make(map[int]*priceSpool)
	consumeErr := consumePriceRows(ctx, rows, stagingDirectory, spools)
	closeErr := closePriceSpools(spools)

	if consumeErr != nil {
		return 0, consumeErr
	}

	if closeErr != nil {
		return 0, closeErr
	}

	years := sortedSpoolYears(spools)

	for _, year := range years {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("update price partitions: %w", err)
		}

		stagedPath := filepath.Join(
			stagingDirectory, strconv.Itoa(year)+".csv",
		)

		if err := store.upsertYear(ctx, year, stagedPath); err != nil {
			return 0, err
		}
	}

	return len(years), nil
}

func (store *csvPriceStore) upsertYear(ctx context.Context, year int, stagedPath string) error {
	path := filepath.Join(store.directory, strconv.Itoa(year)+".csv")
	rows := make(map[observationKey]candidateRow)

	if _, err := os.Stat(path); err == nil {
		if err := loadCandidatePartition(ctx, path, year, rows, nil); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect price partition %s: %w", path, err)
	}

	additions := make(map[observationKey]candidateRow)
	if err := loadCandidatePartition(
		ctx, stagedPath, year, additions, nil,
	); err != nil {
		return fmt.Errorf("merge staged prices for %d: %w", year, err)
	}

	maps.Copy(rows, additions)

	return writeCandidateAtomic(path, sortedRows(rows))
}

func consumePriceRows(
	ctx context.Context,
	rows priceRows,
	directory string,
	spools map[int]*priceSpool,
) error {
	var consumeErr error

	rows(func(row candidateRow, rowErr error) bool {
		if rowErr != nil {
			consumeErr = rowErr

			return false
		}

		if err := ctx.Err(); err != nil {
			consumeErr = fmt.Errorf("stage prices: %w", err)

			return false
		}

		if err := validateCandidateRow(row); err != nil {
			consumeErr = err

			return false
		}

		spool, err := openPriceSpool(directory, row.Date.Year(), spools)
		if err != nil {
			consumeErr = err

			return false
		}

		if err := spool.writer.Write(row.record()); err != nil {
			consumeErr = fmt.Errorf("stage price row: %w", err)

			return false
		}

		return true
	})

	return consumeErr
}

func openPriceSpool(
	directory string,
	year int,
	spools map[int]*priceSpool,
) (*priceSpool, error) {
	if spool := spools[year]; spool != nil {
		return spool, nil
	}

	path := filepath.Join(directory, strconv.Itoa(year)+".csv")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create price spool for %d: %w", year, err)
	}

	spool := &priceSpool{
		file:   file,
		writer: csv.NewWriter(file),
	}
	if err := spool.writer.Write(candidateHeader()); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("write price spool header for %d: %w", year, err)
	}

	spools[year] = spool

	return spool, nil
}

func closePriceSpools(spools map[int]*priceSpool) error {
	var closeErr error

	for year, spool := range spools {
		spool.writer.Flush()

		if err := spool.writer.Error(); err != nil {
			closeErr = errors.Join(
				closeErr, fmt.Errorf("flush price spool for %d: %w", year, err),
			)
		}

		if err := spool.file.Close(); err != nil {
			closeErr = errors.Join(
				closeErr, fmt.Errorf("close price spool for %d: %w", year, err),
			)
		}
	}

	return closeErr
}

func sortedSpoolYears(spools map[int]*priceSpool) []int {
	years := make([]int, 0, len(spools))
	for year := range spools {
		years = append(years, year)
	}

	sort.Ints(years)

	return years
}

func download(
	ctx context.Context,
	client *yahooClient,
	cfg config,
	members []member,
	reader priceReader,
	writer priceWriter,
	logger *slog.Logger,
) (downloadResult, error) {
	latest, err := reader.LatestDates(ctx)
	if err != nil {
		return downloadResult{}, err
	}

	observations := 0
	rows := downloadPriceRows(
		ctx, client, cfg, members, latest, logger, &observations,
	)

	partitions, err := writer.UpsertPrices(ctx, rows)
	if err != nil {
		return downloadResult{}, err
	}

	return downloadResult{
		observations: observations,
		partitions:   partitions,
	}, nil
}

func downloadPriceRows(
	ctx context.Context,
	client *yahooClient,
	cfg config,
	members []member,
	latest map[string]time.Time,
	logger *slog.Logger,
	observations *int,
) priceRows {
	return func(yield func(candidateRow, error) bool) {
		for index, item := range members {
			fetchFrom := cfg.from

			storedThrough := latest[item.Ticker]
			if !storedThrough.IsZero() && !storedThrough.Before(fetchFrom) {
				fetchFrom = storedThrough.AddDate(0, 0, 1)
			}

			if fetchFrom.After(cfg.to) {
				logger.Info(
					"Yahoo prices already current",
					"ticker", item.Ticker,
					"stored_through", storedThrough.Format(dateLayout),
				)

				continue
			}

			logger.Info(
				"fetching Yahoo prices",
				"ticker", item.Ticker,
				"security", index+1,
				"securities", len(members),
				"from", fetchFrom.Format(dateLayout),
				"to", cfg.to.Format(dateLayout),
			)

			prices, err := client.fetch(ctx, item.Ticker, fetchFrom, cfg.to)
			if err != nil {
				yield(candidateRow{}, err)

				return
			}

			for _, price := range prices {
				row := candidateRow{
					CIK:      item.CIK,
					Ticker:   item.Ticker,
					Name:     item.Name,
					Date:     price.Date,
					Open:     price.Open,
					Close:    price.Close,
					AdjClose: price.AdjClose,
				}

				if !yield(row, nil) {
					return
				}

				(*observations)++
			}

			logger.Info(
				"fetched Yahoo prices",
				"ticker", item.Ticker,
				"observations", len(prices),
			)
		}
	}
}

func readManifest(path string) (constituentManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return constituentManifest{}, fmt.Errorf("open manifest %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()

	var manifest constituentManifest
	if err := decoder.Decode(&manifest); err != nil {
		return constituentManifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}

	if err := ensureJSONEOF(decoder); err != nil {
		return constituentManifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}

	if err := validateManifest(&manifest); err != nil {
		return constituentManifest{}, fmt.Errorf("validate manifest %s: %w", path, err)
	}

	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}

		return err
	}

	return nil
}

func validateManifest(manifest *constituentManifest) error {
	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" {
		return errors.New("missing name")
	}

	manifest.AsOf = strings.TrimSpace(manifest.AsOf)
	if _, err := time.Parse(dateLayout, manifest.AsOf); err != nil {
		return fmt.Errorf("invalid as_of date: %w", err)
	}

	manifest.Source = strings.TrimSpace(manifest.Source)
	if manifest.Source == "" {
		return errors.New("missing source")
	}

	if len(manifest.Members) == 0 {
		return errors.New("manifest has no members")
	}

	seen := make(map[string]struct{}, len(manifest.Members))
	for index := range manifest.Members {
		item := &manifest.Members[index]
		item.CIK = strings.TrimSpace(item.CIK)
		item.Ticker = strings.ToUpper(strings.TrimSpace(item.Ticker))
		item.Name = strings.TrimSpace(item.Name)

		if item.CIK == "" || item.Ticker == "" || item.Name == "" {
			return fmt.Errorf("member %d has an empty cik, ticker, or name", index)
		}

		if !cikPattern.MatchString(item.CIK) {
			return fmt.Errorf("member %d has invalid cik %q", index, item.CIK)
		}

		if !tickerPattern.MatchString(item.Ticker) {
			return fmt.Errorf("member %d has invalid ticker %q", index, item.Ticker)
		}

		if _, exists := seen[item.Ticker]; exists {
			return fmt.Errorf("duplicate ticker %s", item.Ticker)
		}

		seen[item.Ticker] = struct{}{}
	}

	return nil
}

func selectMembers(all []member, requested []string) ([]member, error) {
	if len(requested) == 0 {
		return append([]member(nil), all...), nil
	}

	byTicker := make(map[string]member, len(all))
	for _, item := range all {
		byTicker[strings.ToUpper(item.Ticker)] = item
	}

	selected := make([]member, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))

	for _, ticker := range requested {
		if _, duplicate := seen[ticker]; duplicate {
			return nil, fmt.Errorf("ticker %s was requested more than once", ticker)
		}

		item, exists := byTicker[ticker]
		if !exists {
			return nil, fmt.Errorf("ticker %s is not in the manifest", ticker)
		}

		selected = append(selected, item)
		seen[ticker] = struct{}{}
	}

	return selected, nil
}

func loadLatestDates(
	ctx context.Context,
	directory string,
) (map[string]time.Time, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read prices directory: %w", err)
	}

	latest := make(map[string]time.Time)

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read price partitions: %w", err)
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}

		if !yearPattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf(
				"unexpected price partition name %q", entry.Name(),
			)
		}

		year, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".csv"))
		if err != nil {
			return nil, fmt.Errorf("parse partition year: %w", err)
		}

		rows := make(map[observationKey]candidateRow)
		if err := loadCandidatePartition(
			ctx, filepath.Join(directory, entry.Name()), year, rows, latest,
		); err != nil {
			return nil, err
		}
	}

	return latest, nil
}

func loadCandidatePartition(
	ctx context.Context,
	path string,
	year int,
	rows map[observationKey]candidateRow,
	latest map[string]time.Time,
) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open price partition %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)

	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read price partition header %s: %w", path, err)
	}

	if !equalStrings(header, candidateHeader()) {
		return fmt.Errorf("unexpected header in price partition %s", path)
	}

	line := 1

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read price partition %s: %w", path, err)
		}

		line++

		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("read %s line %d: %w", path, line, err)
		}

		row, err := parseCandidateRow(record)
		if err != nil {
			return fmt.Errorf("validate %s line %d: %w", path, line, err)
		}

		if row.Date.Year() != year {
			return fmt.Errorf(
				"date %s is in the %d partition", row.Date.Format(dateLayout), year,
			)
		}

		key := row.key()
		if previous, exists := rows[key]; exists && previous != row {
			return fmt.Errorf(
				"conflicting stored observation for %s on %s",
				row.Ticker, row.Date.Format(dateLayout),
			)
		}

		rows[key] = row
		if latest != nil && row.Date.After(latest[row.Ticker]) {
			latest[row.Ticker] = row.Date
		}
	}

	return nil
}

func parseCandidateRow(record []string) (candidateRow, error) {
	header := candidateHeader()
	if len(record) != len(header) {
		return candidateRow{}, fmt.Errorf(
			"got %d columns, expected %d", len(record), len(header),
		)
	}

	date, err := time.Parse(dateLayout, record[3])
	if err != nil {
		return candidateRow{}, fmt.Errorf("invalid date %q: %w", record[3], err)
	}

	openValue, err := parseStoredPrice("open", record[5])
	if err != nil {
		return candidateRow{}, err
	}

	closeValue, err := parseStoredPrice("close", record[6])
	if err != nil {
		return candidateRow{}, err
	}

	adjusted, err := parseStoredPrice("adjusted close", record[7])
	if err != nil {
		return candidateRow{}, err
	}

	row := candidateRow{
		CIK:      record[0],
		Ticker:   record[1],
		Name:     record[2],
		Date:     date,
		Open:     openValue,
		Close:    closeValue,
		AdjClose: adjusted,
	}
	if record[4] != "USD" || record[8] != "yahoo" {
		return candidateRow{}, errors.New("expected USD currency and yahoo provider")
	}

	if err := validateCandidateRow(row); err != nil {
		return candidateRow{}, err
	}

	return row, nil
}

func validateCandidateRow(row candidateRow) error {
	if !cikPattern.MatchString(row.CIK) || !tickerPattern.MatchString(row.Ticker) ||
		strings.TrimSpace(row.Name) == "" {
		return errors.New("invalid cik, ticker, or name")
	}

	values := []struct {
		name  string
		value float64
	}{
		{name: "open", value: row.Open},
		{name: "close", value: row.Close},
		{name: "adjusted close", value: row.AdjClose},
	}
	for _, value := range values {
		if value.value <= 0 || math.IsNaN(value.value) ||
			math.IsInf(value.value, 0) {
			return fmt.Errorf("invalid %s %v", value.name, value.value)
		}
	}

	return nil
}

func parseStoredPrice(name, value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}

	return parsed, nil
}

func sortedRows(rows map[observationKey]candidateRow) []candidateRow {
	result := make([]candidateRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, row)
	}

	sort.Slice(result, func(left, right int) bool {
		if result[left].Ticker == result[right].Ticker {
			return result[left].Date.Before(result[right].Date)
		}

		return result[left].Ticker < result[right].Ticker
	})

	return result
}

func writeCandidateAtomic(path string, rows []candidateRow) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".prices-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary price partition: %w", err)
	}

	temporaryPath := temporary.Name()

	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("set price partition permissions: %w", err)
	}

	if err := writeCandidateCSV(temporary, rows); err != nil {
		_ = temporary.Close()

		return err
	}

	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("sync price partition: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close price partition: %w", err)
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace price partition %s: %w", path, err)
	}

	return nil
}

func writeCandidateCSV(output io.Writer, rows []candidateRow) error {
	writer := csv.NewWriter(output)
	if err := writer.Write(candidateHeader()); err != nil {
		return fmt.Errorf("write candidate header: %w", err)
	}

	for _, row := range rows {
		if err := writer.Write(row.record()); err != nil {
			return fmt.Errorf("write candidate row: %w", err)
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush candidate file: %w", err)
	}

	return nil
}

func (row candidateRow) key() observationKey {
	return observationKey{
		ticker: row.Ticker,
		date:   row.Date.Format(dateLayout),
	}
}

func (row candidateRow) record() []string {
	return []string{
		row.CIK,
		row.Ticker,
		row.Name,
		row.Date.Format(dateLayout),
		"USD",
		strconv.FormatFloat(row.Open, 'g', -1, 64),
		strconv.FormatFloat(row.Close, 'g', -1, 64),
		strconv.FormatFloat(row.AdjClose, 'g', -1, 64),
		"yahoo",
	}
}

func candidateHeader() []string {
	return []string{
		"cik", "ticker", "name", "date", "currency", "open", "close",
		"adjusted_close", "provider",
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

func safePathComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z':
			return character
		case character >= '0' && character <= '9':
			return character
		case character == '-', character == '_', character == '.':
			return character
		default:
			return '-'
		}
	}, value)

	value = strings.Trim(value, ".-")
	if value == "" {
		return "unnamed"
	}

	return value
}
