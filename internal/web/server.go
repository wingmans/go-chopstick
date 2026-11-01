// Package web serves the temporary local UI for inspecting generated
// filing read models.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"wingman.com/chopstick/internal/constituents"
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
	filingstore "wingman.com/chopstick/internal/web/store"
	"wingman.com/chopstick/internal/xerr"
)

//go:embed static/assets/* static/css/* static/script/*
var staticFiles embed.FS

type Server struct {
	store       filingstore.Store
	rawStore    filingstore.RawStore
	ownership   *ownershipstore.Store
	logger      *slog.Logger
	setName     string
	setAsOf     string
	members     []constituents.Member
	memberByCIK map[string]constituents.Member
	memberByKey map[string][]string
	news        http.Handler
	mux         *http.ServeMux
}

// NewServer creates a server instance with default configuration.
func NewServer(parsedDir string, logger *slog.Logger) *Server {
	server, err := NewConfiguredServer(parsedDir, "", "", logger)
	if err != nil {
		panic(err)
	}

	return server
}

// NewConfiguredServer creates a dashboard with an optional constituent set.
// The set is only a human-name lookup; CIK and accession remain filing keys.
func NewConfiguredServer(parsedDir, setDir, setName string, logger *slog.Logger) (*Server, error) {
	return NewConfiguredServerWithDB(context.Background(), parsedDir,
		filepath.Join(filepath.Dir(parsedDir), "analysis.db"), setDir, setName,
		logger)
}

// NewConfiguredServerWithDB creates a dashboard backed by an explicit
// normalized-view database.
func NewConfiguredServerWithDB(ctx context.Context, parsedDir, databasePath,
	setDir, setName string, logger *slog.Logger,
) (*Server, error) {
	return NewConfiguredServerWithDatabases(ctx, parsedDir, databasePath,
		filepath.Join(filepath.Dir(databasePath), "ownership.db"), setDir,
		setName, logger)
}

// NewConfiguredServerWithDatabases creates a dashboard backed by explicit
// filing-analysis and ownership databases.
func NewConfiguredServerWithDatabases(ctx context.Context, parsedDir,
	databasePath, ownershipPath, setDir, setName string, logger *slog.Logger,
) (*Server, error) {
	store, err := filingstore.NewSQLiteStore(ctx, parsedDir, databasePath)
	if err != nil {
		return nil, err
	}

	var ownershipStore *ownershipstore.Store
	if _, err := os.Stat(ownershipPath); err == nil {
		ownershipStore, err = ownershipstore.Open(ctx, ownershipPath)
		if err != nil {
			_ = store.Close()

			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = store.Close()

		return nil, err
	}

	server := &Server{
		store:       store,
		rawStore:    store,
		ownership:   ownershipStore,
		logger:      logger,
		setName:     setName,
		setAsOf:     "",
		memberByCIK: map[string]constituents.Member{},
		memberByKey: map[string][]string{},
	}
	server.mux = server.routes()

	if setName == "" {
		return server, nil
	}

	set, err := constituents.LoadJSON(filepath.Join(setDir, setName+".json"))
	if err != nil {
		_ = store.Close()
		if ownershipStore != nil {
			_ = ownershipStore.Close()
		}

		return nil, err
	}

	server.setAsOf = set.AsOf

	server.members = append(server.members, set.Members...)
	for _, member := range set.Members {
		cik := canonicalCIK(member.CIK)
		server.memberByCIK[cik] = member
		server.memberByKey[strings.ToLower(member.Ticker)] = append(
			server.memberByKey[strings.ToLower(member.Ticker)], cik)
		server.memberByKey[strings.ToLower(member.Name)] = append(
			server.memberByKey[strings.ToLower(member.Name)], cik)
	}

	return server, nil
}

// MountNews adds the filing-news module to the application at /news.
func (s *Server) MountNews(handler http.Handler) {
	s.news = handler
}

// Close releases the database used by the server.
func (s *Server) Close() error {
	var closeErrors []error
	if s.ownership != nil {
		closeErrors = append(closeErrors, s.ownership.Close())
	}

	if closer, ok := s.store.(interface{ Close() error }); ok {
		closeErrors = append(closeErrors, closer.Close())
	}

	return errors.Join(closeErrors...)
}

func isWorkspaceRequest(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Hx-Request"), "true") {
		return false
	}

	target := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Hx-Target")), "#")

	return target == "workspace"
}

func (s *Server) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)

		return
	}

	if _, err := w.Write(append(data, '\n')); err != nil && s.logger != nil {
		s.logger.Debug("write JSON response failed", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	structured := xerr.Wrap(xerr.Internal, "DASHBOARD_REQUEST_FAILED", message, err)

	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
		message = "filing not found"
		structured = xerr.Wrap(xerr.NotFound, "FILING_NOT_FOUND", message, err)
	}

	if s.logger != nil {
		s.logger.Error("local dashboard request failed", "error", structured, "code", structured.Code, "status", status)
	}

	http.Error(w, message, status)
}
