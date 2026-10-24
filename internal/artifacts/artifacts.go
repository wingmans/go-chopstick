package artifacts

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
)

const (
	// MaxExtractedBytes is the hard ceiling for one inspection bundle.
	MaxExtractedBytes int64 = 300 * 1024 * 1024
	manifestName            = "manifest.json"
)

// Selection identifies documents to extract.
type Selection struct {
	All       bool
	Types     []string
	Filenames []string
}

// Manifest identifies a generated directory and supports safe cleanup.
type Manifest struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedBy   string             `json:"generated_by"`
	GeneratedAt   string             `json:"generated_at"`
	CIK           string             `json:"cik"`
	Accession     string             `json:"accession"`
	SourceSHA256  string             `json:"source_sha256"`
	TotalBytes    int64              `json:"total_bytes"`
	Documents     []ManifestDocument `json:"documents"`
}

// ManifestDocument describes one extracted source document.
type ManifestDocument struct {
	Type        string `json:"type"`
	Sequence    string `json:"sequence"`
	Filename    string `json:"filename"`
	Description string `json:"description,omitempty"`
	Bytes       int64  `json:"bytes"`
	Decoded     bool   `json:"decoded"`
}

// Extract creates a new disposable directory. It never overwrites an existing
// bundle or modifies the source submission.
func Extract(ctx context.Context, filing *edgar.ParsedFiling, source,
	destination string, selection Selection,
) (Manifest, error) {
	if filing == nil {
		return Manifest{}, errors.New("parsed filing is required")
	}

	documents := selectDocuments(filing.Documents, selection)
	if len(documents) == 0 {
		return Manifest{}, errors.New("no source documents matched the selection")
	}

	if _, err := os.Stat(destination); err == nil {
		return Manifest{}, fmt.Errorf("artifact directory already exists: %s",
			destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("inspect artifact directory: %w", err)
	}

	input, err := os.Open(source) // #nosec G304 -- caller resolves known source.
	if err != nil {
		return Manifest{}, fmt.Errorf("open source submission: %w", err)
	}
	defer func() { _ = input.Close() }()

	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return Manifest{}, fmt.Errorf("create artifact parent: %w", err)
	}

	temporary, err := os.MkdirTemp(filepath.Dir(destination), ".artifacts-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create artifact staging directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(temporary) }()

	manifest := Manifest{
		SchemaVersion: 1, GeneratedBy: "chopstick-edgar-artifacts",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		CIK:         filing.Metadata.CIK, Accession: filing.Metadata.Accession,
		SourceSHA256: filing.SourceSHA256,
		Documents:    make([]ManifestDocument, 0, len(documents)),
	}

	seen := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}

		filename, err := safeFilename(document.Filename)
		if err != nil {
			return Manifest{}, err
		}

		if _, exists := seen[filename]; exists {
			return Manifest{}, fmt.Errorf("duplicate artifact filename %q",
				filename)
		}

		seen[filename] = struct{}{}

		outputPath := filepath.Join(temporary, filename)

		written, decoded, err := extractDocument(input, document, outputPath,
			MaxExtractedBytes-manifest.TotalBytes)
		if err != nil {
			return Manifest{}, fmt.Errorf("extract %s: %w", filename, err)
		}

		manifest.TotalBytes += written
		manifest.Documents = append(manifest.Documents, ManifestDocument{
			Type: document.Type, Sequence: document.Sequence,
			Filename: filename, Description: document.Description,
			Bytes: written, Decoded: decoded,
		})
	}

	if err := writeManifest(temporary, manifest); err != nil {
		return Manifest{}, err
	}

	if err := os.Rename(temporary, destination); err != nil {
		return Manifest{}, fmt.Errorf("publish artifact directory: %w", err)
	}

	return manifest, nil
}

// Clean removes only a directory carrying a matching generated manifest.
func Clean(destination, cik, accession string) error {
	data, err := os.ReadFile(filepath.Join(destination, manifestName))
	if err != nil {
		return fmt.Errorf("read artifact manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode artifact manifest: %w", err)
	}

	if manifest.GeneratedBy != "chopstick-edgar-artifacts" ||
		manifest.CIK != cik || manifest.Accession != accession {
		return errors.New("artifact manifest does not match cleanup target")
	}

	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("remove artifact directory: %w", err)
	}

	return nil
}

func selectDocuments(documents []edgar.SubmissionDocument,
	selection Selection,
) []edgar.SubmissionDocument {
	types := make(map[string]struct{}, len(selection.Types))
	for _, value := range selection.Types {
		types[strings.ToUpper(strings.TrimSpace(value))] = struct{}{}
	}

	filenames := make(map[string]struct{}, len(selection.Filenames))
	for _, value := range selection.Filenames {
		filenames[strings.TrimSpace(value)] = struct{}{}
	}

	result := make([]edgar.SubmissionDocument, 0)

	for _, document := range documents {
		_, typeMatch := types[strings.ToUpper(document.Type)]

		_, filenameMatch := filenames[document.Filename]
		if selection.All || typeMatch || filenameMatch {
			result = append(result, document)
		}
	}

	return result
}

func extractDocument(source *os.File, document edgar.SubmissionDocument,
	destination string, remaining int64,
) (int64, bool, error) {
	if remaining <= 0 || document.ContentLength < 0 ||
		document.ContentOffset < 0 {
		return 0, false, errors.New("300 MiB extraction limit exceeded")
	}

	section := io.NewSectionReader(source, document.ContentOffset,
		document.ContentLength)

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600)
	if err != nil {
		return 0, false, err
	}

	defer func() { _ = output.Close() }()

	limited := &limitWriter{writer: output, remaining: remaining}

	decoded := strings.EqualFold(document.Type, "GRAPHIC")
	if decoded {
		err = decodeUU(section, limited)
	} else {
		_, err = io.Copy(limited, section)
	}

	if err != nil {
		return limited.written, decoded, err
	}

	if err := output.Close(); err != nil {
		return limited.written, decoded, err
	}

	return limited.written, decoded, nil
}

type limitWriter struct {
	writer    io.Writer
	remaining int64
	written   int64
}

func (w *limitWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("300 MiB extraction limit exceeded")
	}

	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	w.written += int64(n)

	return n, err
}

func decodeUU(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	started := false

	for scanner.Scan() {
		line := scanner.Text()
		if !started {
			if strings.HasPrefix(line, "begin ") {
				started = true
			}

			continue
		}

		if line == "end" {
			return nil
		}

		if line == "" || line == "`" || line == " " {
			continue
		}

		length := int((line[0] - 32) & 63)
		encoded := line[1:]

		expected := ((length + 2) / 3) * 4
		if missing := expected - len(encoded); missing > 0 {
			// EDGAR submissions sometimes trim trailing zero-padding spaces.
			encoded += strings.Repeat("`", missing)
		}

		decoded := make([]byte, 0, length)
		for len(encoded) >= 4 && len(decoded) < length {
			a := (encoded[0] - 32) & 63
			b := (encoded[1] - 32) & 63
			c := (encoded[2] - 32) & 63
			d := (encoded[3] - 32) & 63
			decoded = append(decoded, a<<2|b>>4, b<<4|c>>2, c<<6|d)
			encoded = encoded[4:]
		}

		if len(decoded) < length {
			return errors.New("truncated uuencoded line")
		}

		if _, err := writer.Write(decoded[:length]); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if !started {
		return errors.New("GRAPHIC document is not uuencoded")
	}

	return errors.New("uuencoded document has no end marker")
}

func safeFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." ||
		filepath.Base(filename) != filename || strings.Contains(filename, `\`) {
		return "", fmt.Errorf("unsafe artifact filename %q", filename)
	}

	return filename, nil
}

func writeManifest(directory string, manifest Manifest) error {
	file, err := os.OpenFile(filepath.Join(directory, manifestName),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact manifest: %w", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("write artifact manifest: %w", err)
	}

	return file.Close()
}
