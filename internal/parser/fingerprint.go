package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fingerprintSuffix = ".fingerprint.json"

// SourceFingerprint is the small cache marker for one raw SEC submission.
type SourceFingerprint struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mod_time_unix_nano"`
}

// FingerprintPath returns the sidecar path for a raw submission.
func FingerprintPath(source string) string {
	return source + fingerprintSuffix
}

// WriteSourceFingerprint atomically records the current raw source metadata.
func WriteSourceFingerprint(source string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat source for fingerprint: %w", err)
	}

	path := FingerprintPath(source)
	temporary, err := os.CreateTemp(filepath.Dir(path), ".fingerprint-*.part")
	if err != nil {
		return fmt.Errorf("create source fingerprint: %w", err)
	}

	temporaryPath := temporary.Name()
	defer func() { _ = temporary.Close(); _ = os.Remove(temporaryPath) }()

	fingerprint := SourceFingerprint{
		Size: info.Size(), ModTime: info.ModTime().UnixNano(),
	}
	if err := json.NewEncoder(temporary).Encode(fingerprint); err != nil {
		return fmt.Errorf("encode source fingerprint: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync source fingerprint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close source fingerprint: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("store source fingerprint: %w", err)
	}

	return nil
}

// SourceFingerprintMatches compares a raw source with its sidecar.
func SourceFingerprintMatches(ctx context.Context, source string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	info, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("stat source for fingerprint: %w", err)
	}

	file, err := os.Open(FingerprintPath(source))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open source fingerprint: %w", err)
	}
	defer func() { _ = file.Close() }()

	var fingerprint SourceFingerprint
	if err := json.NewDecoder(file).Decode(&fingerprint); err != nil {
		return false, fmt.Errorf("decode source fingerprint: %w", err)
	}

	return info.Size() == fingerprint.Size &&
		info.ModTime().UnixNano() == fingerprint.ModTime, nil
}
