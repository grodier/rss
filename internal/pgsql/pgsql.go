package pgsql

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/lib/pq"
)

type DB struct {
	dsn string
	db  *sql.DB

	MaxOpenConnections int
	MaxIdleConnections int
	MaxIdleTime        time.Duration
}

func NewDB(dsn string) *DB {
	return &DB{dsn: dsn}
}

func (pg *DB) Open() error {
	db, err := sql.Open("postgres", pg.dsn)
	if err != nil {
		return err
	}
	pg.db = db

	pg.db.SetMaxOpenConns(pg.MaxOpenConnections)
	pg.db.SetMaxIdleConns(pg.MaxIdleConnections)
	pg.db.SetConnMaxIdleTime(pg.MaxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = pg.db.PingContext(ctx)
	if err != nil {
		pg.db.Close()
		return err
	}

	return nil
}

func (pg *DB) Close() error {
	if pg.db != nil {
		return pg.db.Close()
	}

	return nil
}
