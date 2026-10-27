package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// querier is satisfied by *sql.DB and *sql.Tx, so helpers taking it can run
// inside or outside a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// OpenDB opens a pool of connections to dsn and pings it. Every connection
// starts with JIT off: Postgres would compile the timeline queries for their
// high estimated cost, which adds tens of milliseconds and saves nothing on
// queries this size.
func OpenDB(dsn string, maxOpenConns, maxIdleConns int, maxIdleTime time.Duration) (*sql.DB, error) {
	cfg, err := pq.NewConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.Runtime == nil {
		cfg.Runtime = map[string]string{}
	}
	cfg.Runtime["jit"] = "off"
	connector, err := pq.NewConnectorConfig(cfg)
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
