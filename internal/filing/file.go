package filing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const viewFilename = "filing-view.json"

// Path returns the compatibility JSON path for a filing view.
func Path(directory string, view View) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", errors.New("filing view directory is empty")
	}

	cik := strings.TrimSpace(view.Metadata.CIK)
	if len(cik) == 0 || len(cik) > 10 || !decimalDigits(cik) {
		return "", fmt.Errorf("invalid filing CIK %q", view.Metadata.CIK)
	}

	accession := strings.TrimSpace(view.Metadata.Accession)

	parts := strings.Split(accession, "-")
	if len(parts) != 3 || len(parts[0]) != 10 || len(parts[1]) != 2 ||
		len(parts[2]) != 6 || !decimalDigits(strings.Join(parts, "")) {
		return "", fmt.Errorf("invalid filing accession %q", accession)
	}

	paddedCIK := strings.Repeat("0", 10-len(cik)) + cik

	return filepath.Join(directory, paddedCIK, accession, viewFilename), nil
}

// Save atomically writes a filing view to its compatibility JSON path.
func Save(directory string, view View) (string, error) {
	path, err := Path(directory, view)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create filing view directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".filing-view-*.part")
	if err != nil {
		return "", fmt.Errorf("create filing view temporary file: %w", err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = temporary.Close(); _ = os.Remove(temporaryName) }()

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(view); err != nil {
		return "", fmt.Errorf("encode filing view: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync filing view: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close filing view: %w", err)
	}

	if err := os.Rename(temporaryName, path); err != nil {
		return "", fmt.Errorf("store filing view: %w", err)
	}

	return path, nil
}

// Load reads a persisted filing view.
func Load(path string) (View, error) {
	file, err := os.Open(path)
	if err != nil {
		return View{}, err
	}
	defer func() { _ = file.Close() }()

	var view View
	if err := json.NewDecoder(file).Decode(&view); err != nil {
		return View{}, fmt.Errorf("decode filing view: %w", err)
	}

	if view.SchemaVersion != SchemaVersion {
		return View{}, fmt.Errorf(
			"unsupported filing view schema %d", view.SchemaVersion)
	}

	return view, nil
}

func decimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}

	return true
}
