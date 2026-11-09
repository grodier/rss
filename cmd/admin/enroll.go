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
	"time"

	"github.com/grodier/rss/internal/admin"
	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// enrollCodeAttempts is how many first codes enroll reads before giving up.
const enrollCodeAttempts = 3

// runEnroll runs "admin enroll -email <email> [-reset] [flags]": it enrolls
// an admin with the password on the first line of stdin, shows the TOTP
// secret's provisioning URI and QR code, reads a first code from the next
// line to confirm the authenticator app has it, and prints the recovery
// codes, shown once. With -reset it replaces an existing admin's password,
// secret and codes instead. It returns the exit code.
func runEnroll(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	var dsn, key, email string
	var reset bool

	fs := flag.NewFlagSet("admin enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: admin enroll -email <email> [-reset] [-db-dsn <dsn>] [-secret-key <key>]")
		fmt.Fprintln(stderr, "The password is read from the first line of stdin, then the first code from the next.")
		fs.PrintDefaults()
	}
	fs.StringVar(&dsn, "db-dsn", "", "PostgreSQL DSN")
	fs.StringVar(&key, "secret-key", "", secretKeyUsage+"; the admin site's own")
	fs.StringVar(&email, "email", "", "The admin's email")
	fs.BoolVar(&reset, "reset", false, "Replace an existing admin's password, TOTP secret and recovery codes")

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

	in := bufio.NewReader(stdin)
	password, err := readLine(in)
	if err != nil {
		fmt.Fprintf(stderr, "read password: %v\n", err)
		return 1
	}

	var v validator.Validator
	v.CheckField(validator.NotBlank(email), "email", "Email is required")
	v.CheckField(validator.Matches(email, validator.EmailRX), "email", "Email must be a valid email address")
	v.CheckNewPassword("password", password, email)
	sealKey, keyErr := parseSecretKey(key)
	if dsn == "" || keyErr != nil || !v.Valid() {
		if dsn == "" {
			fmt.Fprintln(stderr, "database DSN must not be empty")
		}
		if key == "" {
			fmt.Fprintln(stderr, "-secret-key (RSS_SECRET_KEY) must be set: the admin site's key, which opens the secret")
		} else if keyErr != nil {
			fmt.Fprintln(stderr, keyErr)
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

	codes, err := enroll(context.Background(), psql.NewAdminRepository(db), sealKey, email, password, reset, in, stdout, time.Now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Recovery codes, each of which works once instead of a code. They're shown only now: keep them somewhere safe.")
	for _, c := range codes {
		fmt.Fprintln(stdout, "  "+c)
	}
	if reset {
		fmt.Fprintf(stdout, "\nReset %s: the old password, authenticator entry and recovery codes no longer work.\n", email)
	} else {
		fmt.Fprintf(stdout, "\nEnrolled %s.\n", email)
	}
	return 0
}

// enroll checks the admin exists (reset) or doesn't, shows the new secret on
// stdout, and reads codes from in until one confirms it, then stores the
// admin and returns its recovery codes.
func enroll(ctx context.Context, admins admin.AdminStore, key []byte, email, password string, reset bool, in *bufio.Reader, stdout io.Writer, now func() time.Time) ([]string, error) {
	// Only someone with the database and the key can run this, so it may
	// say whether the email is an admin's.
	_, err := admins.GetByEmail(ctx, email)
	switch {
	case err == nil && !reset:
		return nil, fmt.Errorf("an admin with email %s already exists; use -reset to replace their password, secret and recovery codes", email)
	case errors.Is(err, rss.ErrNoRecord) && reset:
		return nil, fmt.Errorf("there's no admin with email %s to reset", email)
	case err != nil && !errors.Is(err, rss.ErrNoRecord):
		return nil, err
	}

	e, err := admin.NewEnrollment(email, password)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(stdout, "Scan this QR code with your authenticator app, or add this URI to it:")
	fmt.Fprintln(stdout)
	if code, err := encodeQR([]byte(e.URI())); err == nil {
		if err := writeQR(stdout, code); err != nil {
			return nil, err
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintln(stdout, e.URI())

	for range enrollCodeAttempts {
		fmt.Fprint(stdout, "\nEnter the code your app shows: ")
		line, err := readLine(in)
		if err != nil {
			return nil, fmt.Errorf("read code: %w", err)
		}
		if line == "" {
			return nil, errors.New("no code entered; nothing was saved")
		}
		codes, err := e.Finish(ctx, admins, key, line, reset, now())
		if errors.Is(err, admin.ErrWrongCode) {
			fmt.Fprintln(stdout, "That code didn't work. Check the app's clock, or wait for the next code.")
			continue
		}
		if errors.Is(err, rss.ErrDuplicateEmail) {
			return nil, fmt.Errorf("an admin with email %s already exists", email)
		}
		if err != nil {
			return nil, err
		}
		return codes, nil
	}
	return nil, fmt.Errorf("no code worked after %d tries; nothing was saved", enrollCodeAttempts)
}

// readLine returns the next line of r, without its "\n" or "\r\n". It
// keeps other whitespace, which is valid in passwords. At the end of r it
// returns what's left, "" if nothing.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r"), nil
}
