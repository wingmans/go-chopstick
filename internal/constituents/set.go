// Package constituents contains current constituent-set data and validation.
package constituents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const CIKWidth = 10

// Member identifies a current constituent for EDGAR processing.
type Member struct {
	CIK    string `json:"cik"`
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
}

// Set is a snapshot of a named constituent set.
type Set struct {
	Name    string   `json:"name"`
	AsOf    string   `json:"as_of"`
	Source  string   `json:"source"`
	Members []Member `json:"members"`
}

// NormalizeCIK returns the SEC's ten-digit representation of a CIK.
func NormalizeCIK(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("CIK is empty")
	}

	if strings.Trim(value, "0123456789") != "" {
		return "", fmt.Errorf("CIK %q is not numeric", value)
	}

	if len(value) > CIKWidth {
		return "", fmt.Errorf("CIK %q exceeds %d digits", value, CIKWidth)
	}

	return strings.Repeat("0", CIKWidth-len(value)) + value, nil
}

// Validate checks that a set is safe to use as a processing filter.
func (s *Set) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("set name is empty")
	}

	if strings.TrimSpace(s.AsOf) == "" {
		return errors.New("set as_of is empty")
	}

	if _, err := time.Parse("2006-01-02", s.AsOf); err != nil {
		return fmt.Errorf("invalid set as_of %q: %w", s.AsOf, err)
	}

	if len(s.Members) == 0 {
		return errors.New("set has no members")
	}

	seenTicker := make(map[string]struct{}, len(s.Members))
	for i := range s.Members {
		member := &s.Members[i]

		cik, err := NormalizeCIK(member.CIK)
		if err != nil {
			return fmt.Errorf("member %d: %w", i, err)
		}

		member.CIK = cik
		member.Ticker = strings.ToUpper(strings.TrimSpace(member.Ticker))

		member.Name = strings.TrimSpace(member.Name)
		if member.Ticker == "" {
			return fmt.Errorf("member %d: ticker is empty", i)
		}

		if member.Name == "" {
			return fmt.Errorf("member %s: name is empty", member.Ticker)
		}

		if _, exists := seenTicker[member.Ticker]; exists {
			return fmt.Errorf("duplicate ticker %s", member.Ticker)
		}

		seenTicker[member.Ticker] = struct{}{}
	}

	sort.Slice(s.Members, func(i, j int) bool {
		return s.Members[i].Ticker < s.Members[j].Ticker
	})

	return nil
}

// WriteJSON validates and writes a readable JSON snapshot.
func (s *Set) WriteJSON(w io.Writer) error {
	if err := s.Validate(); err != nil {
		return err
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(s); err != nil {
		return fmt.Errorf("encode constituent set: %w", err)
	}

	return nil
}

// LoadJSON reads and validates a constituent-set snapshot from disk.
func LoadJSON(path string) (*Set, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open constituent set %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var set Set
	if err := json.NewDecoder(file).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode constituent set %s: %w", path, err)
	}

	if err := set.Validate(); err != nil {
		return nil, fmt.Errorf("validate constituent set %s: %w", path, err)
	}

	return &set, nil
}

// CIKs returns the unique normalized CIKs in the set.
func (s *Set) CIKs() []string {
	seen := make(map[string]struct{}, len(s.Members))

	ciks := make([]string, 0, len(s.Members))
	for _, member := range s.Members {
		if _, exists := seen[member.CIK]; exists {
			continue
		}

		seen[member.CIK] = struct{}{}
		ciks = append(ciks, member.CIK)
	}

	sort.Strings(ciks)

	return ciks
}
