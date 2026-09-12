package edgar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ParsedPath validates source-controlled identifiers before constructing a path.
func (f *ParsedFiling) ParsedPath(directory string) (string, error) {
	if directory == "" {
		return "", errors.New("parsed directory is required")
	}

	if len(f.Metadata.CIK) > 10 || !decimalDigits(f.Metadata.CIK) {
		return "", errors.New("invalid filing CIK")
	}

	parts := strings.Split(f.Metadata.Accession, "-")
	if len(parts) != 3 || len(parts[0]) != 10 || len(parts[1]) != 2 || len(parts[2]) != 6 {
		return "", errors.New("invalid filing accession")
	}

	for _, part := range parts {
		if !decimalDigits(part) {
			return "", errors.New("invalid filing accession")
		}
	}

	return filepath.Join(directory, f.Metadata.CIK, f.Metadata.Accession, "filing.json"), nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}

	return true
}

// SaveParsedFiling atomically replaces one parsed result. Source files are untouched.
func SaveParsedFiling(directory string, filing *ParsedFiling) (string, error) {
	if filing == nil {
		return "", errors.New("parsed filing is nil")
	}

	path, err := filing.ParsedPath(directory)
	if err != nil {
		return "", err
	}

	if filing.SchemaVersion != ParsedSchemaVersion || filing.ParserVersion != ParserVersion {
		return "", errors.New("cannot save an incompatible parsed filing version")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create parsed directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".filing-*.part")
	if err != nil {
		return "", fmt.Errorf("create parsed temporary file: %w", err)
	}

	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(filing); err != nil {
		return "", fmt.Errorf("encode parsed filing: %w", err)
	}

	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync parsed filing: %w", err)
	}

	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close parsed filing: %w", err)
	}

	if err := os.Rename(file.Name(), path); err != nil {
		return "", fmt.Errorf("store parsed filing: %w", err)
	}

	return path, nil
}

func LoadParsedFiling(path string) (*ParsedFiling, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open parsed filing: %w", err)
	}
	defer func() { _ = file.Close() }()

	var filing ParsedFiling

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&filing); err != nil {
		return nil, fmt.Errorf("decode parsed filing: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after parsed filing")
	}

	if filing.SchemaVersion != ParsedSchemaVersion {
		return nil, fmt.Errorf("unsupported parsed schema version %d", filing.SchemaVersion)
	}

	if _, err := filing.ParsedPath("."); err != nil {
		return nil, err
	}

	return &filing, nil
}

// MatchesSource checks whether versions and source bytes permit reuse.
// A matching partial result still needs its Status and Diagnostics inspected.
func (f *ParsedFiling) MatchesSource(ctx context.Context, path string) (bool, error) {
	if f.SchemaVersion != ParsedSchemaVersion || f.ParserVersion != ParserVersion {
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open source for checksum: %w", err)
	}

	defer func() { _ = file.Close() }()

	hash := sha256.New()
	buffer := make([]byte, 64*1024)

	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		n, err := file.Read(buffer)
		_, _ = hash.Write(buffer[:n])

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return false, fmt.Errorf("hash source: %w", err)
		}
	}

	return f.SourceSHA256 == hex.EncodeToString(hash.Sum(nil)), nil
}
