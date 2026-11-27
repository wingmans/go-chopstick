// Package news maintains the disposable read model used by the news page.
package news

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wingman.com/chopstick/internal/edgar/news/dbgen"
	"wingman.com/chopstick/internal/platform/ctxlog"
	"wingman.com/chopstick/internal/platform/sqlite"
)

const maxSQLiteInteger = uint64(1<<63 - 1)

// Item is one filing in discovery order. Archived rows use negative
// sequences so they can coexist with events from a recreated stream.
type Item struct {
	Sequence        int64
	AccessionNumber string
	CIK             string
	CompanyName     string
	FormType        string
	FilingDate      string
	FilingURL       string
	FilingPath      string
	SourceFeed      string
	Selected        bool
	SelectionReason string
	SelectionMode   string
	SelectionSet    string
}

// Filter limits the disposable news read model.
type Filter struct {
	Query     string
	CIK       string
	FormType  string
	Selection string
	From      string
	To        string
	Limit     int
}

// Store owns the news SQLite read model.
type Store struct {
	db      *sql.DB
	queries *dbgen.Queries
}

// Open opens the read model. The boolean reports whether it must be replayed.
func Open(ctx context.Context, path string) (*Store, bool, error) {
	return open(ctx, path, true)
}

// OpenExisting opens a provisioned news database.
func OpenExisting(ctx context.Context, path string) (*Store, error) {
	store, _, err := open(ctx, path, false)

	return store, err
}

// InspectSchema checks the schema of an existing news database.
func InspectSchema(ctx context.Context, path string) error {
	return sqlite.InspectSchema(ctx, path, newsSchemaVersion)
}

func open(ctx context.Context, path string, initialize bool) (*Store, bool, error) {
	const options = "_busy_timeout=5000&_txlock=immediate"

	dsn := path + "?_journal_mode=WAL&" + options
	if !initialize {
		existing, err := sqlite.ExistingDSN(path, options)
		if err != nil {
			return nil, false, fmt.Errorf("open news database: %w", err)
		}

		dsn = existing
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, false, fmt.Errorf("open news database: %w", err)
	}

	db.SetMaxOpenConns(1)

	var created bool

	if initialize {
		created, err = createSchema(ctx, db)
	} else {
		err = sqlite.CheckSchema(ctx, db, newsSchemaVersion)
	}

	if err != nil {
		_ = db.Close()

		return nil, false, err
	}

	if created {
		ctxlog.FromContext(ctx).Info("database created", "database", "news",
			"path", path, "schema_version", newsSchemaVersion)
	}

	queries, err := dbgen.Prepare(ctx, db)
	if err != nil {
		_ = db.Close()

		return nil, false, fmt.Errorf("prepare news queries: %w", err)
	}

	return &Store{db: db, queries: queries}, created, nil
}

// Apply stores an event after it has been durably received from JetStream.
func (s *Store) Apply(ctx context.Context, sequence uint64, eventID string,
	item Item,
) error {
	if sequence > maxSQLiteInteger {
		return fmt.Errorf("store news item: sequence %d exceeds SQLite integer",
			sequence)
	}

	err := s.queries.ApplyNewsItem(ctx, dbgen.ApplyNewsItemParams{
		StreamSequence:  int64(sequence),
		EventID:         eventID,
		AccessionNumber: item.AccessionNumber,
		CIK:             item.CIK,
		CompanyName:     item.CompanyName,
		FormType:        item.FormType,
		FilingDate:      item.FilingDate,
		FilingUrl:       item.FilingURL,
		FilingPath:      item.FilingPath,
		SourceFeed:      item.SourceFeed,
		Selected:        boolInt(item.Selected),
		SelectionReason: item.SelectionReason,
		SelectionMode:   item.SelectionMode,
		SelectionSet:    item.SelectionSet,
	})
	if err != nil {
		return fmt.Errorf("store news item: %w", err)
	}

	return nil
}

// List returns newest discoveries first. Dates are filing dates, not observed
// or processing timestamps.
func (s *Store) List(ctx context.Context, filter Filter) ([]Item, error) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}

	queryPattern := ""
	if filter.Query != "" {
		queryPattern = "%" + filter.Query + "%"
	}

	rows, err := s.queries.ListNewsItems(ctx, dbgen.ListNewsItemsParams{
		QueryPattern: queryPattern,
		CIK:          filter.CIK,
		FormType:     filter.FormType,
		Selected:     selectionValue(filter.Selection),
		FromDate:     filter.From,
		ToDate:       filter.To,
		RowLimit:     int64(filter.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query news items: %w", err)
	}

	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, itemFromListRow(row))
	}

	return items, nil
}

// Get returns one observed filing for an explicit on-demand request.
func (s *Store) Get(ctx context.Context, accession string) (Item, error) {
	row, err := s.queries.NewsItem(ctx, accession)
	if err != nil {
		return Item{}, fmt.Errorf("read news item: %w", err)
	}

	return itemFromRow(row), nil
}

func itemFromListRow(row dbgen.ListNewsItemsRow) Item {
	return Item{
		Sequence:        row.StreamSequence,
		AccessionNumber: row.AccessionNumber,
		CIK:             row.CIK,
		CompanyName:     row.CompanyName,
		FormType:        row.FormType,
		FilingDate:      row.FilingDate,
		FilingURL:       row.FilingUrl,
		FilingPath:      row.FilingPath,
		SourceFeed:      row.SourceFeed,
		Selected:        row.Selected != 0,
		SelectionReason: row.SelectionReason,
		SelectionMode:   row.SelectionMode,
		SelectionSet:    row.SelectionSet,
	}
}

func itemFromRow(row dbgen.NewsItemRow) Item {
	return Item{
		Sequence:        row.StreamSequence,
		AccessionNumber: row.AccessionNumber,
		CIK:             row.CIK,
		CompanyName:     row.CompanyName,
		FormType:        row.FormType,
		FilingDate:      row.FilingDate,
		FilingURL:       row.FilingUrl,
		FilingPath:      row.FilingPath,
		SourceFeed:      row.SourceFeed,
		Selected:        row.Selected != 0,
		SelectionReason: row.SelectionReason,
		SelectionMode:   row.SelectionMode,
		SelectionSet:    row.SelectionSet,
	}
}

func selectionValue(selection string) int64 {
	switch selection {
	case "selected":
		return 1
	case "skipped":
		return 0
	default:
		return -1
	}
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}

	return 0
}

// Close closes the disposable read model.
func (s *Store) Close() error {
	return errors.Join(s.queries.Close(), s.db.Close())
}
