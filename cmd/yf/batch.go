package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/marketstore"
	"wingman.com/fetch-ecb/internal/marketworker"
)

const (
	batchRecordPrice  = "price"
	batchRecordAction = "action"
)

type batchWriter interface {
	Write(
		ctx context.Context,
		item marketstore.WorkItem,
		prices []marketstore.Price,
		actions []marketstore.CorporateAction,
	) (string, error)
}

type batchReader interface {
	Read(
		ctx context.Context,
		item marketstore.WorkItem,
		path string,
	) ([]marketstore.Price, []marketstore.CorporateAction, error)
}

// batchStore combines reading and writing capabilities for CSV batches.
type batchStore interface {
	batchWriter
	batchReader
	Recover(
		ctx context.Context,
		item marketstore.WorkItem,
	) (marketworker.Recovery, error)
}

type csvBatchStore struct {
	root string
}

func newCSVBatchStore(root string) (*csvBatchStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("CSV batch directory is required")
	}

	return &csvBatchStore{root: filepath.Clean(root)}, nil
}

func (store *csvBatchStore) Write(
	ctx context.Context,
	item marketstore.WorkItem,
	prices []marketstore.Price,
	actions []marketstore.CorporateAction,
) (string, error) {
	if !tickerPattern.MatchString(item.Security.Symbol) {
		return "", fmt.Errorf(
			"unsafe Yahoo symbol for CSV batch: %q", item.Security.Symbol,
		)
	}

	if !validBatchKind(item.Kind) {
		return "", fmt.Errorf("invalid Yahoo CSV work type %q", item.Kind)
	}

	directory := filepath.Join(store.root, item.Security.Symbol)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create Yahoo CSV batch directory: %w", err)
	}

	path := filepath.Join(directory, batchFilename(item))

	temporary, err := os.CreateTemp(directory, ".incoming-*.csv")
	if err != nil {
		return "", fmt.Errorf("create Yahoo CSV batch: %w", err)
	}

	temporaryPath := temporary.Name()
	removeTemporary := true

	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	writer := csv.NewWriter(temporary)
	writeErr := writeBatchRows(ctx, writer, item, prices, actions)
	writer.Flush()

	writeErr = errors.Join(writeErr, writer.Error())
	if writeErr == nil {
		writeErr = temporary.Sync()
	}

	closeErr := temporary.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", fmt.Errorf("write Yahoo CSV batch: %w", err)
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("publish Yahoo CSV batch: %w", err)
	}

	removeTemporary = false

	return path, nil
}

func writeBatchRows(ctx context.Context, writer *csv.Writer, item marketstore.WorkItem,
	prices []marketstore.Price, actions []marketstore.CorporateAction,
) error {
	if err := writer.Write(batchCSVHeader()); err != nil {
		return err
	}

	for _, price := range prices {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write Yahoo CSV prices: %w", err)
		}

		record := []string{
			batchRecordPrice,
			item.Security.ID,
			item.Security.Symbol,
			price.Date.UTC().Format(dateLayout),
			"USD",
			formatOptionalPriceFloat(price.Open),
			formatFloat(price.Close),
			formatFloat(price.AdjustedClose),
			"",
			"",
			"",
			"",
			"",
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write Yahoo CSV actions: %w", err)
		}

		record := []string{
			batchRecordAction,
			item.Security.ID,
			item.Security.Symbol,
			action.Date.UTC().Format(dateLayout),
			"USD",
			"",
			"",
			"",
			action.Type,
			formatOptionalFloat(action.Amount),
			formatOptionalFloat(action.Numerator),
			formatOptionalFloat(action.Denominator),
			action.SourceID,
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func (store *csvBatchStore) Read(ctx context.Context, item marketstore.WorkItem, path string) (
	[]marketstore.Price, []marketstore.CorporateAction, error,
) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open Yahoo CSV batch: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	expectedHeader := batchCSVHeader()
	reader.FieldsPerRecord = len(expectedHeader)

	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read Yahoo CSV batch header: %w", err)
	}

	if !equalStrings(header, expectedHeader) {
		return nil, nil, errors.New("unexpected Yahoo CSV batch header")
	}

	var (
		prices  []marketstore.Price
		actions []marketstore.CorporateAction
	)

	seenPrices := make(map[string]struct{})
	seenActions := make(map[string]struct{})

	for line := 2; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("read Yahoo CSV batch: %w", err)
		}

		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, nil, fmt.Errorf(
				"read Yahoo CSV batch line %d: %w", line, err,
			)
		}

		if record[1] != item.Security.ID ||
			record[2] != item.Security.Symbol || record[4] != "USD" {
			return nil, nil, fmt.Errorf(
				"yahoo CSV batch identity mismatch on line %d", line,
			)
		}

		date, err := time.Parse(dateLayout, record[3])
		if err != nil {
			return nil, nil, fmt.Errorf(
				"parse Yahoo CSV batch date on line %d: %w", line, err,
			)
		}

		switch record[0] {
		case batchRecordPrice:
			price, err := parseBatchPrice(record, date)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", line, err)
			}

			dateKey := date.Format(dateLayout)
			if _, exists := seenPrices[dateKey]; exists {
				return nil, nil, fmt.Errorf(
					"duplicate Yahoo CSV price date %s", dateKey,
				)
			}

			seenPrices[dateKey] = struct{}{}

			prices = append(prices, price)
		case batchRecordAction:
			action, err := parseBatchAction(record, date)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", line, err)
			}

			if _, exists := seenActions[action.SourceID]; exists {
				return nil, nil, fmt.Errorf(
					"duplicate Yahoo CSV action %s", action.SourceID,
				)
			}

			seenActions[action.SourceID] = struct{}{}

			actions = append(actions, action)
		default:
			return nil, nil, fmt.Errorf(
				"unknown Yahoo CSV record type %q on line %d",
				record[0], line,
			)
		}
	}

	return prices, actions, nil
}

type storedBatch struct {
	item marketstore.WorkItem
	path string
}

// Recover reconstructs the longest covered prefix represented by retained
// CSV batches. It is used only when a security has lost all database prices.
func (store *csvBatchStore) Recover(
	ctx context.Context,
	item marketstore.WorkItem,
) (marketworker.Recovery, error) {
	directory := filepath.Join(store.root, item.Security.Symbol)

	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return marketworker.Recovery{}, nil
	}

	if err != nil {
		return marketworker.Recovery{}, fmt.Errorf(
			"read Yahoo CSV batch directory: %w", err,
		)
	}

	batches := make([]storedBatch, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		batch, ok := parseStoredBatch(item, directory, entry.Name())
		if ok {
			batches = append(batches, batch)
		}
	}

	selected, coveredFrom, coveredThrough := recoveryChain(item, batches)
	if len(selected) == 0 {
		return marketworker.Recovery{}, nil
	}

	priceByDate := make(map[string]marketstore.Price)
	actionByID := make(map[string]marketstore.CorporateAction)
	paths := make([]string, 0, len(selected))

	for _, batch := range selected {
		prices, actions, err := store.Read(ctx, batch.item, batch.path)
		if err != nil {
			return marketworker.Recovery{}, fmt.Errorf(
				"recover Yahoo CSV batch %s: %w", batch.path, err,
			)
		}

		for _, price := range prices {
			priceByDate[price.Date.Format(dateLayout)] = price
		}

		for _, action := range actions {
			actionByID[action.SourceID] = action
		}

		paths = append(paths, batch.path)
	}

	prices := make([]marketstore.Price, 0, len(priceByDate))
	for _, price := range priceByDate {
		prices = append(prices, price)
	}

	sort.Slice(prices, func(left, right int) bool {
		return prices[left].Date.Before(prices[right].Date)
	})

	actions := make([]marketstore.CorporateAction, 0, len(actionByID))
	for _, action := range actionByID {
		actions = append(actions, action)
	}

	sort.Slice(actions, func(left, right int) bool {
		if !actions[left].Date.Equal(actions[right].Date) {
			return actions[left].Date.Before(actions[right].Date)
		}

		return actions[left].SourceID < actions[right].SourceID
	})

	return marketworker.Recovery{
		From:    coveredFrom,
		Through: coveredThrough,
		Prices:  prices,
		Actions: actions,
		Paths:   paths,
	}, nil
}

func parseStoredBatch(
	template marketstore.WorkItem,
	directory string,
	name string,
) (storedBatch, bool) {
	if filepath.Ext(name) != ".csv" {
		return storedBatch{}, false
	}

	parts := strings.Split(strings.TrimSuffix(name, ".csv"), "_")
	if len(parts) != 3 {
		return storedBatch{}, false
	}

	from, fromErr := time.Parse(dateLayout, parts[1])
	through, throughErr := time.Parse(dateLayout, parts[2])
	kind := marketstore.WorkKind(parts[0])

	if fromErr != nil || throughErr != nil || through.Before(from) ||
		!validBatchKind(kind) || through.After(template.Through) {
		return storedBatch{}, false
	}

	item := template
	item.Kind = kind
	item.From = from
	item.Through = through

	return storedBatch{item: item, path: filepath.Join(directory, name)}, true
}

func recoveryChain(
	item marketstore.WorkItem,
	batches []storedBatch,
) ([]storedBatch, time.Time, time.Time) {
	baseIndex := -1

	for index, batch := range batches {
		isSnapshot := batch.item.Kind == marketstore.WorkBootstrap ||
			batch.item.Kind == marketstore.WorkReconcile
		if !isSnapshot || batch.item.From.After(item.From) {
			continue
		}

		if baseIndex == -1 ||
			batch.item.Through.After(batches[baseIndex].item.Through) {
			baseIndex = index
		}
	}

	if baseIndex == -1 {
		return nil, time.Time{}, time.Time{}
	}

	selected := []storedBatch{batches[baseIndex]}
	coveredFrom := batches[baseIndex].item.From
	coveredThrough := batches[baseIndex].item.Through

	for {
		nextIndex := -1

		for index, batch := range batches {
			if batch.item.Kind != marketstore.WorkIncremental ||
				!batch.item.Through.After(coveredThrough) ||
				batch.item.From.After(coveredThrough.AddDate(0, 0, 1)) {
				continue
			}

			if nextIndex == -1 ||
				batch.item.Through.After(batches[nextIndex].item.Through) {
				nextIndex = index
			}
		}

		if nextIndex == -1 {
			break
		}

		selected = append(selected, batches[nextIndex])
		coveredThrough = batches[nextIndex].item.Through
	}

	// Repairs are applied last so their corrected observations win when rows
	// are folded into the recovered batch.
	for _, batch := range batches {
		if batch.item.Kind == marketstore.WorkRepair &&
			!batch.item.From.Before(coveredFrom) &&
			!batch.item.Through.After(coveredThrough) {
			selected = append(selected, batch)
		}
	}

	return selected, coveredFrom, coveredThrough
}

func parseBatchPrice(record []string, date time.Time) (marketstore.Price, error) {
	if record[8] != "" || record[9] != "" || record[10] != "" ||
		record[11] != "" || record[12] != "" {
		return marketstore.Price{}, errors.New("price has action values")
	}

	openValue, openValid, err := parseOptionalPriceFloat("open", record[5])
	if err != nil {
		return marketstore.Price{}, err
	}

	var open *float64
	if openValid {
		open = &openValue
	}

	closeValue, err := parseRequiredFloat("close", record[6])
	if err != nil {
		return marketstore.Price{}, err
	}

	adjustedClose, err := parseRequiredFloat("adjusted close", record[7])
	if err != nil {
		return marketstore.Price{}, err
	}

	return marketstore.Price{
		Date:          date,
		Open:          open,
		Close:         closeValue,
		AdjustedClose: adjustedClose,
	}, nil
}

func parseBatchAction(record []string, date time.Time) (marketstore.CorporateAction, error) {
	if record[5] != "" || record[6] != "" || record[7] != "" {
		return marketstore.CorporateAction{}, errors.New(
			"corporate action has price values",
		)
	}

	if record[12] == "" {
		return marketstore.CorporateAction{}, errors.New(
			"corporate action has no source ID",
		)
	}

	action := marketstore.CorporateAction{
		SourceID: record[12],
		Date:     date,
		Type:     record[8],
	}

	var err error

	switch action.Type {
	case "dividend":
		action.Amount, err = parseRequiredFloat("dividend amount", record[9])
		if err == nil && (record[10] != "" || record[11] != "") {
			err = errors.New("dividend has split values")
		}
	case "split":
		if record[9] != "" {
			err = errors.New("split has a dividend amount")

			break
		}

		action.Numerator, err = parseRequiredFloat("split numerator", record[10])
		if err == nil {
			action.Denominator, err = parseRequiredFloat(
				"split denominator", record[11],
			)
		}
	default:
		err = fmt.Errorf("unknown corporate action type %q", action.Type)
	}

	if err != nil {
		return marketstore.CorporateAction{}, err
	}

	return action, nil
}

func parseRequiredFloat(name, value string) (float64, error) {
	if value == "" {
		return 0, fmt.Errorf("missing %s", name)
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}

	return parsed, nil
}

func parseOptionalPriceFloat(name, value string) (float64, bool, error) {
	if value == "" {
		return 0, false, nil
	}

	parsed, err := parseRequiredFloat(name, value)
	if err != nil {
		return 0, false, err
	}

	return parsed, true, nil
}

func batchCSVHeader() []string {
	return []string{
		"record_type",
		"security_id",
		"symbol",
		"date",
		"currency",
		"open",
		"close",
		"adjusted_close",
		"action_type",
		"amount",
		"numerator",
		"denominator",
		"source_id",
	}
}

func batchFilename(item marketstore.WorkItem) string {
	return fmt.Sprintf(
		"%s_%s_%s.csv", item.Kind,
		item.From.UTC().Format(dateLayout),
		item.Through.UTC().Format(dateLayout),
	)
}

func validBatchKind(kind marketstore.WorkKind) bool {
	switch kind {
	case marketstore.WorkBootstrap,
		marketstore.WorkIncremental,
		marketstore.WorkReconcile,
		marketstore.WorkRepair:
		return true
	default:
		return false
	}
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatOptionalPriceFloat(value *float64) string {
	if value == nil {
		return ""
	}

	return formatFloat(*value)
}

func formatOptionalFloat(value float64) string {
	if value == 0 {
		return ""
	}

	return formatFloat(value)
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
