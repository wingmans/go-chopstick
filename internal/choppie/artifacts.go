package choppie

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/chopstick/internal/artifacts"
	"wingman.com/chopstick/internal/parser"
)

type artifactCommandConfig struct {
	operation    string
	cik          string
	accession    string
	parsedDir    string
	filingsDir   string
	artifactsDir string
	address      string
	types        []string
	filenames    []string
	all          bool
}

func runArtifacts(ctx context.Context, cfg artifactCommandConfig) error {
	destination := filepath.Join(cfg.artifactsDir, cfg.cik, cfg.accession)
	if cfg.operation == "serve" {
		return serveArtifacts(ctx, cfg, destination)
	}

	if cfg.operation == "clean" {
		if err := artifacts.Clean(destination, cfg.cik, cfg.accession); err != nil {
			return err
		}

		fmt.Printf("removed %s\n", destination)

		return nil
	}

	parsedPath := filepath.Join(cfg.parsedDir, cfg.cik, cfg.accession,
		"filing.json")

	filing, err := parser.LoadParsedFiling(parsedPath)
	if err != nil {
		return err
	}

	if cfg.operation == "list" {
		fmt.Println("SEQ\tTYPE\tBYTES\tFILENAME\tDESCRIPTION")

		for _, document := range filing.Documents {
			fmt.Printf("%s\t%s\t%d\t%s\t%s\n", document.Sequence,
				document.Type, document.ContentLength, document.Filename,
				document.Description)
		}

		return nil
	}

	source, err := artifactSourcePath(cfg.filingsDir, filing)
	if err != nil {
		return err
	}

	matches, err := parser.SourceFingerprintMatches(ctx, source)
	if err != nil {
		return err
	}

	if !matches {
		return errors.New("raw source changed since parsing; parse it again")
	}

	manifest, err := artifacts.Extract(ctx, filing, source, destination,
		artifacts.Selection{
			All: cfg.all, Types: cfg.types, Filenames: cfg.filenames,
		})
	if err != nil {
		return err
	}

	fmt.Printf("extracted %d documents (%d bytes) to %s\n",
		len(manifest.Documents), manifest.TotalBytes, destination)

	return nil
}

func validateViewerAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("--address must be host:port: %w", err)
	}

	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("--address must use a loopback host")
		}
	}

	return nil
}

func serveArtifacts(ctx context.Context, cfg artifactCommandConfig,
	destination string,
) error {
	handler, err := artifacts.Viewer(destination, cfg.cik, cfg.accession)
	if err != nil {
		return err
	}

	listenConfig := net.ListenConfig{}

	listener, err := listenConfig.Listen(ctx, "tcp", cfg.address)
	if err != nil {
		return fmt.Errorf("listen for artifact viewer: %w", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				5*time.Second)
			defer cancel()

			_ = server.Shutdown(shutdownCtx)
		case <-done:
		}
	}()

	defer close(done)

	fmt.Printf("viewing %s at http://%s (press Ctrl-C to stop)\n",
		destination, listener.Addr())

	if err := server.Serve(listener); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve artifact viewer: %w", err)
	}

	return nil
}

func artifactSourcePath(filingsDir string, filing *parser.ParsedFiling,
) (string, error) {
	switch filing.SourceBase {
	case "absolute":
		return filing.SourcePath, nil
	case "filings":
		path := filepath.Join(filingsDir, filepath.FromSlash(filing.SourcePath))

		relative, err := filepath.Rel(filingsDir, path)
		if err != nil || !filepath.IsLocal(relative) {
			return "", errors.New("parsed source escapes the filings directory")
		}

		return path, nil
	default:
		return "", fmt.Errorf("unsupported parsed source base %q",
			filing.SourceBase)
	}
}
