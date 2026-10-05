package main

import (
	"errors"
	"fmt"
	"time"
)

type config struct {
	env    string
	server serverConfig
	db     dbConfig
	lookup lookupConfig
}

type serverConfig struct {
	port int
}

type lookupConfig struct {
	workers int
}

type dbConfig struct {
	dsn          string
	maxOpenConns int
	maxIdleConns int
	maxIdleTime  time.Duration
}

func defaultConfig() config {
	return config{
		env: "development",
		server: serverConfig{
			port: 8080,
		},
		db: dbConfig{
			maxOpenConns: 25,
			maxIdleConns: 25,
			maxIdleTime:  15 * time.Minute,
		},
		lookup: lookupConfig{
			workers: 2,
		},
	}
}

func (c config) Validate() error {
	var errs []error
	if c.env != "development" && c.env != "production" {
		errs = append(errs, fmt.Errorf("invalid environment %q: must be development or production", c.env))
	}
	if c.server.port < 1 || c.server.port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port %d: must be 1-65535", c.server.port))
	}
	if c.db.dsn == "" {
		errs = append(errs, errors.New("database DSN must not be empty"))
	}
	if c.lookup.workers < 0 || c.lookup.workers > 16 {
		errs = append(errs, fmt.Errorf("invalid lookup workers %d: must be 0-16", c.lookup.workers))
	}
	return errors.Join(errs...)
}
