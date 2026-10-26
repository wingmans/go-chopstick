package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	cikPattern    = regexp.MustCompile(`^[0-9]{10}$`)
	tickerPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]*$`)
)

type member struct {
	SecurityID string `json:"security_id,omitempty"`
	CIK        string `json:"cik"`
	Ticker     string `json:"ticker"`
	Name       string `json:"name"`
}

type constituentManifest struct {
	Name    string   `json:"name"`
	AsOf    string   `json:"as_of"`
	Source  string   `json:"source"`
	Members []member `json:"members"`
}

// readManifest reads and validates a constituent manifest from the given path.
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

	seenTickers := make(map[string]struct{}, len(manifest.Members))
	seenSecurityIDs := make(map[string]struct{}, len(manifest.Members))

	for index := range manifest.Members {
		item := &manifest.Members[index]
		item.CIK = strings.TrimSpace(item.CIK)
		item.Ticker = strings.ToUpper(strings.TrimSpace(item.Ticker))
		item.Name = strings.TrimSpace(item.Name)
		item.SecurityID = strings.TrimSpace(item.SecurityID)

		if item.CIK == "" || item.Ticker == "" || item.Name == "" {
			return fmt.Errorf("member %d has an empty cik, ticker, or name", index)
		}

		if !cikPattern.MatchString(item.CIK) {
			return fmt.Errorf("member %d has invalid cik %q", index, item.CIK)
		}

		if !tickerPattern.MatchString(item.Ticker) {
			return fmt.Errorf("member %d has invalid ticker %q", index, item.Ticker)
		}

		if item.SecurityID == "" {
			item.SecurityID = item.CIK + ":" + item.Ticker
		}

		if _, exists := seenTickers[item.Ticker]; exists {
			return fmt.Errorf("duplicate ticker %s", item.Ticker)
		}

		if _, exists := seenSecurityIDs[item.SecurityID]; exists {
			return fmt.Errorf("duplicate security_id %s", item.SecurityID)
		}

		seenTickers[item.Ticker] = struct{}{}
		seenSecurityIDs[item.SecurityID] = struct{}{}
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
