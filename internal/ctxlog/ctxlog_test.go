package ctxlog

import (
	"log/slog"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]slog.Level{
		"trace":    slog.LevelDebug - 4,
		"debug":    slog.LevelDebug,
		"info":     slog.LevelInfo,
		"warn":     slog.LevelWarn,
		"error":    slog.LevelError,
		"critical": slog.LevelError + 4,
		"off":      slog.Level(127),
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			if got := parseLogLevel(input); got != want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", input, got, want)
			}
		})
	}
}
