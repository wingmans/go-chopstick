package edgar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChecksumRecord describes a locally stored index file without changing it.
type ChecksumRecord struct {
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	Source    string    `json:"source"`
	SourceURL string    `json:"source_url,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// ChecksumPath returns the sidecar path for an index file.
func ChecksumPath(indexPath string) string {
	extension := filepath.Ext(indexPath)

	return strings.TrimSuffix(indexPath, extension) + "-CHK.txt"
}

func writeChecksum(indexPath, source, sourceURL string) (string, error) {
	file, err := os.Open(indexPath)
	if err != nil {
		return "", fmt.Errorf("open index for checksum %s: %w", indexPath, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat index for checksum %s: %w", indexPath, err)
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash index %s: %w", indexPath, err)
	}

	record := ChecksumRecord{
		Path:      filepath.Clean(indexPath),
		SizeBytes: info.Size(),
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		Source:    source,
		SourceURL: sourceURL,
		CheckedAt: time.Now().UTC(),
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode checksum %s: %w", indexPath, err)
	}

	data = append(data, '\n')

	checksumPath := ChecksumPath(indexPath)

	temporary, err := os.CreateTemp(filepath.Dir(checksumPath), ".checksum-*.part")
	if err != nil {
		return "", fmt.Errorf("create checksum %s: %w", checksumPath, err)
	}

	temporaryName := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}

	if _, err := temporary.Write(data); err != nil {
		cleanup()

		return "", fmt.Errorf("write checksum %s: %w", checksumPath, err)
	}

	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)

		return "", fmt.Errorf("close checksum %s: %w", checksumPath, err)
	}

	if err := os.Rename(temporaryName, checksumPath); err != nil {
		_ = os.Remove(temporaryName)

		return "", fmt.Errorf("store checksum %s: %w", checksumPath, err)
	}

	return checksumPath, nil
}
