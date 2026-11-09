package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// runCreateUser runs "admin create-user -name <name> -email <email> [flags]":
// it creates a reader's account with the password on the first line of stdin,
// and returns the exit code. It checks the account with the same rules as
// signup. Like runEnroll, it writes flag errors and the -help output to
// stderr.
func runCreateUser(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	var dsn, name, email string

	fs := flag.NewFlagSet("admin create-user", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: admin create-user -name <name> -email <email> [-db-dsn <dsn>]")
		fmt.Fprintln(stderr, "The password is read from the first line of stdin.")
		fs.PrintDefaults()
	}
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")
	fs.StringVar(&name, "name", name, "The account's name")
	fs.StringVar(&email, "email", email, "The account's email")

	if err := cli.SetFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// fs.Parse reports its own errors to stderr.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	password, err := readLine(bufio.NewReader(stdin))
	if err != nil {
		fmt.Fprintf(stderr, "read password: %v\n", err)
		return 1
	}

	name = strings.TrimSpace(name)
	var v validator.Validator
	v.CheckNewAccount(name, email, password)
	if dsn == "" || !v.Valid() {
		if dsn == "" {
			fmt.Fprintln(stderr, "database DSN must not be empty")
		}
		for _, field := range slices.Sorted(maps.Keys(v.FieldErrors)) {
			fmt.Fprintf(stderr, "%s: %s\n", field, v.FieldErrors[field])
		}
		return 2
	}

	cfg := defaultConfig().db
	db, err := psql.OpenDB(dsn, cfg.maxOpenConns, cfg.maxIdleConns, cfg.maxIdleTime)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()

	_, _, err = psql.NewUserRepository(db).Create(context.Background(), name, email, password)
	// Unlike signup, this may say the email is taken: only someone with access
	// to the database can run it.
	if errors.Is(err, rss.ErrDuplicateEmail) {
		fmt.Fprintf(stderr, "an account with email %s already exists\n", email)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintf(stdout, "created account for %s\n", email)
	return 0
}
