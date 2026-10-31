// Package envfile loads local development environment files.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Load adds values from path to the environment without overriding values
// already supplied by the shell or process supervisor. A missing file is not
// an error.
func Load(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("open environment file: %w", err)
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, value, found := strings.Cut(line, "=")

		name = strings.TrimSpace(strings.TrimPrefix(name, "export "))
		if !found || name == "" {
			return fmt.Errorf("invalid environment line %q", line)
		}

		if _, exists := os.LookupEnv(name); exists {
			continue
		}

		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}

		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s from environment file: %w", name, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read environment file: %w", err)
	}

	return nil
}
