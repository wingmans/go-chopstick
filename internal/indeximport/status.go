package indeximport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const importStateVersion = 1

// Inventory describes the locally available and durably imported index data.
type Inventory struct {
	Directory        string
	MasterPath       string
	AvailablePeriods []string
	MissingPeriods   []string
	ImportedPeriods  []string
	MasterState      string
	LastImport       *ImportSummary
}

// ImportSummary records one index selection committed to the discovery store.
type ImportSummary struct {
	CommittedAt time.Time `json:"committed_at"`
	Files       []string  `json:"files"`
	Periods     []string  `json:"periods"`
	Examined    int       `json:"examined"`
	Queued      int       `json:"queued"`
	Duplicates  int       `json:"duplicates"`
}

type importState struct {
	Version int               `json:"version"`
	Periods map[string]string `json:"periods"`
	Last    *ImportSummary    `json:"last,omitempty"`
}

// Inspect reports index inventory without changing the local cache.
func Inspect(indexesDirectory, masterPath string) (Inventory, error) {
	result := Inventory{
		Directory:  indexesDirectory,
		MasterPath: masterPath,
	}

	paths, err := localQuarterlyFiles(indexesDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Inventory{}, err
	}

	result.AvailablePeriods = periodsForFiles(paths)
	result.MissingPeriods = missingPeriods(result.AvailablePeriods)

	result.MasterState, err = inspectMaster(masterPath, paths)
	if err != nil {
		return Inventory{}, err
	}

	state, err := readImportState(importStatePath(masterPath))
	if err != nil {
		return Inventory{}, err
	}

	result.ImportedPeriods = make([]string, 0, len(state.Periods))
	for period := range state.Periods {
		result.ImportedPeriods = append(result.ImportedPeriods, period)
	}

	sort.Strings(result.ImportedPeriods)
	result.LastImport = state.Last

	return result, nil
}

func inspectMaster(masterPath string, quarterly []string) (string, error) {
	_, err := os.Stat(masterPath)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}

	if err != nil {
		return "", fmt.Errorf("inspect master index: %w", err)
	}

	if len(quarterly) == 0 {
		return "unverifiable", nil
	}

	stale, err := masterNeedsStitch(masterPath, quarterly)
	if err != nil {
		return "", err
	}

	if stale {
		return "stale", nil
	}

	return "current", nil
}

func recordImport(masterPath string, files []string,
	summary ImportSummary,
) error {
	path := importStatePath(masterPath)

	state, err := readImportState(path)
	if err != nil {
		return err
	}

	if state.Periods == nil {
		state.Periods = make(map[string]string)
	}

	summary.Files = baseNames(files)
	summary.Periods = periodsForFiles(files)

	for _, period := range summary.Periods {
		state.Periods[period] = summary.CommittedAt.Format(time.RFC3339Nano)
	}

	state.Version = importStateVersion
	state.Last = &summary

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create index state directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".import-state-*.json")
	if err != nil {
		return fmt.Errorf("create temporary index state: %w", err)
	}

	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("protect temporary index state: %w", err)
	}

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(state); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("encode index state: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary index state: %w", err)
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace index state: %w", err)
	}

	return nil
}

func readImportState(path string) (importState, error) {
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return importState{
			Version: importStateVersion,
			Periods: make(map[string]string),
		}, nil
	}

	if err != nil {
		return importState{}, fmt.Errorf("read index state: %w", err)
	}

	var state importState
	if err := json.Unmarshal(payload, &state); err != nil {
		return importState{}, fmt.Errorf("decode index state: %w", err)
	}

	if state.Version != importStateVersion {
		return importState{}, fmt.Errorf(
			"index state version is %d, want %d",
			state.Version, importStateVersion,
		)
	}

	return state, nil
}

func importStatePath(masterPath string) string {
	return filepath.Join(filepath.Dir(masterPath), "import-state.json")
}

func periodsForFiles(paths []string) []string {
	periods := make([]string, 0, len(paths))
	for _, path := range paths {
		match := quarterlyFilePattern.FindStringSubmatch(filepath.Base(path))
		if len(match) == 3 {
			periods = append(periods, match[1]+"-QTR"+match[2])
		}
	}

	sort.Strings(periods)

	return periods
}

func baseNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}

	return names
}

func missingPeriods(periods []string) []string {
	if len(periods) < 2 {
		return nil
	}

	available := make(map[int]struct{}, len(periods))
	first := periodOrdinal(periods[0])

	last := periodOrdinal(periods[len(periods)-1])

	for _, period := range periods {
		available[periodOrdinal(period)] = struct{}{}
	}

	missing := make([]string, 0)

	for ordinal := first; ordinal <= last; ordinal++ {
		if _, found := available[ordinal]; !found {
			missing = append(missing, periodFromOrdinal(ordinal))
		}
	}

	return missing
}

func periodOrdinal(period string) int {
	match := quarterlyFilePattern.FindStringSubmatch(period + ".tsv")
	if len(match) != 3 {
		return 0
	}

	year, _ := strconv.Atoi(match[1])
	quarter, _ := strconv.Atoi(match[2])

	return year*4 + quarter - 1
}

func periodFromOrdinal(ordinal int) string {
	year := ordinal / 4
	quarter := ordinal%4 + 1

	return fmt.Sprintf("%04d-QTR%d", year, quarter)
}
