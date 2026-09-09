// Package ctxlog stores a standard logger in a context.
package ctxlog

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
)

type loggerKeyType struct{}

type Option func(*options)

type options struct {
	levelOverride *string
}

func LoggerKey() loggerKeyType {
	return loggerKeyType{}
}

func New(opts ...Option) *slog.Logger {
	o := &options{levelOverride: nil}

	for _, opt := range opts {
		opt(o)
	}

	level := slog.LevelInfo

	if o.levelOverride != nil {
		level = parseLogLevel(*o.levelOverride)
	} else if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		level = parseLogLevel(raw)
	}

	return slog.New(tint.NewTextHandler(os.Stdout, tintOptions(level)))
}

func NewLoggerFromEnv() *slog.Logger {
	raw := os.Getenv("LOG_LEVEL")

	level := slog.LevelInfo
	if raw != "" {
		level = parseLogLevel(raw)
	}

	return slog.New(tint.NewTextHandler(os.Stdout, tintOptions(level)))
}

func tintOptions(level slog.Level) *tint.Options {
	return &tint.Options{
		Level:      level,
		AddSource:  false,
		TimeFormat: "",
		NoColor:    false,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if strings.ToLower(a.Key) == "id" {
				return slog.Attr{
					Key:   a.Key,
					Value: slog.StringValue(colorCyan + a.Value.String() + colorReset),
				}
			}

			return a
		},
	}
}

func WithLevelOverride(lvl string) Option {
	return func(o *options) {
		o.levelOverride = &lvl
	}
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if ctx == nil || logger == nil {
		return ctx
	}

	return context.WithValue(ctx, LoggerKey(), logger)
}

// FromContext returns a logger from context.Context.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}

	if log, ok := ctx.Value(LoggerKey()).(*slog.Logger); ok && log != nil {
		return log
	}

	return slog.Default()
}

// WithFields adds a field to the context.Context.
func WithFields(ctx context.Context, attrs ...any) context.Context {
	log := FromContext(ctx)
	log = log.With(attrs...)

	return context.WithValue(ctx, LoggerKey(), log)
}

// MustFromContext generates a panic when the logger is missing:
// panic: ctxlog: logger missing from context.
func MustFromContext(ctx context.Context) *slog.Logger {
	log := FromContext(ctx)
	if log == slog.Default() {
		panic("ctxlog: logger missing from context")
	}

	return log
}

// ParseLogLevel maps string config values to slog.Level. default vakue is INFO.
func parseLogLevel(raw string) slog.Level {
	if raw == "" {
		return slog.LevelInfo
	}

	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

const (
	colorReset = "\x1b[0m"
	colorCyan  = "\x1b[36m"
)
