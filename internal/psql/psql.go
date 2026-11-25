package psql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
)

// querier is satisfied by *sql.DB and *sql.Tx, so helpers taking it can run
// inside or outside a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// NewConnector returns a connector to dsn whose connections start with JIT
// off: Postgres would compile the timeline queries for their high estimated
// cost, which adds tens of milliseconds and saves nothing on queries this
// size. OpenDB uses it; so does anything that should measure the app's
// queries as the app runs them.
func NewConnector(dsn string) (*pq.Connector, error) {
	cfg, err := pq.NewConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.Runtime == nil {
		cfg.Runtime = map[string]string{}
	}
	cfg.Runtime["jit"] = "off"
	return pq.NewConnectorConfig(cfg)
}

// OpenDB opens a pool of connections to dsn (see NewConnector) and pings it.
func OpenDB(dsn string, maxOpenConns, maxIdleConns int, maxIdleTime time.Duration) (*sql.DB, error) {
	connector, err := NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxIdleTime(maxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// maxErrorLen caps the error messages stored by LookupRepository.Fail,
// FeedRepository.RecordFailure, EnrichRepository.Retry,
// ImportRepository.Finish and EmailRepository's MarkFailed and GiveUp, in
// bytes.
const maxErrorLen = 1000

// execOne runs stmt and returns rss.ErrNoRecord if it changed no rows.
func execOne(ctx context.Context, q querier, stmt string, args ...any) error {
	res, err := q.ExecContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return rss.ErrNoRecord
	}
	return nil
}

// isUniqueViolation reports whether err is Postgres's unique_violation (23505).
func isUniqueViolation(err error) bool {
	return hasPgCode(err, pqerror.UniqueViolation)
}

// isForeignKeyViolation reports whether err is Postgres's foreign_key_violation (23503).
func isForeignKeyViolation(err error) bool {
	return hasPgCode(err, pqerror.ForeignKeyViolation)
}

// hasPgCode reports whether err is a Postgres error with code.
func hasPgCode(err error, code pqerror.Code) bool {
	pqErr, ok := errors.AsType[*pq.Error](err)
	return ok && pqErr.Code == code
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern escapes LIKE wildcards in q and wraps it for a substring match.
func likePattern(q string) string {
	return "%" + likeEscaper.Replace(q) + "%"
}

// prefixPattern escapes LIKE wildcards in q for a match at the start.
func prefixPattern(q string) string {
	return likeEscaper.Replace(q) + "%"
}

// wordPattern escapes LIKE wildcards in q for a match at the start of a
// word: after a space, in a text with a space put in front of it.
func wordPattern(q string) string {
	return "% " + likeEscaper.Replace(q) + "%"
}
