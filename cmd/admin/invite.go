package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// inviteUsage lists admin invite's subcommands.
const inviteUsage = `Usage:
  admin invite create [-email <email>] [-note <text>] [-expires 336h]
                                 create a signup invite and print its link
  admin invite list              list invites, newest first
  admin invite revoke <id>       revoke an invite, so its link stops working
`

// defaultInviteExpiry is how long an invite works unless -expires says.
const defaultInviteExpiry = 14 * 24 * time.Hour

// runInvite runs "admin invite <subcommand>" and returns the exit code, like
// run's.
func runInvite(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, inviteUsage)
		return 2
	}
	switch args[0] {
	case "create":
		return runInviteCreate(args[1:], getenv, stdout, stderr)
	case "list":
		return runInviteList(args[1:], getenv, stdout, stderr)
	case "revoke":
		return runInviteRevoke(args[1:], getenv, stdout, stderr)
	case "help", "-help", "-h":
		fmt.Fprint(stdout, inviteUsage)
		return 0
	}
	fmt.Fprintf(stderr, "admin invite: unknown command %q\n%s", args[0], inviteUsage)
	return 2
}

// parseInviteFlags reads fs's flags from args and, for those not on the
// command line, from RSS_ environment variables. It returns the exit code
// to stop with, or -1 to go on.
func parseInviteFlags(fs *flag.FlagSet, args []string, getenv func(string) string, stderr io.Writer) int {
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
	return -1
}

// openInviteDB opens the database at dsn with the admin's pool settings.
func openInviteDB(dsn string) (*sql.DB, error) {
	cfg := defaultConfig().db
	return psql.OpenDB(dsn, cfg.maxOpenConns, cfg.maxIdleConns, cfg.maxIdleTime)
}

// checkAppURL returns appURL without a trailing slash if it's an absolute
// http or https URL with no path, query or fragment.
func checkAppURL(appURL string) (string, error) {
	appURL = strings.TrimSuffix(appURL, "/")
	u, err := url.Parse(appURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("invalid app URL %q: must be an absolute http or https URL with no path", appURL)
	}
	return appURL, nil
}

// runInviteCreate runs "admin invite create": it stores an invite and prints
// its link. With -email, the invite is bound to that confirmed waitlist
// entry: only its address can sign up with it, and the account starts
// confirmed. Without, it's open, and -note says who it's for.
func runInviteCreate(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var dsn, appURL, email, note string
	expires := defaultInviteExpiry

	fs := flag.NewFlagSet("admin invite create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: admin invite create [-email <email>] [-note <text>] [-expires 336h]")
		fs.PrintDefaults()
	}
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")
	fs.StringVar(&appURL, "app-url", "http://localhost:8080", "The reader's public URL, for the invite's link")
	fs.StringVar(&email, "email", email, "Bind the invite to the confirmed waitlist entry with this email (default: an open invite)")
	fs.StringVar(&note, "note", note, "Who the invite is for, shown only in the admin")
	fs.DurationVar(&expires, "expires", expires, "How long the invite works")
	if code := parseInviteFlags(fs, args, getenv, stderr); code >= 0 {
		return code
	}

	var errs []string
	if dsn == "" {
		errs = append(errs, "database DSN must not be empty")
	}
	appURL, err := checkAppURL(appURL)
	if err != nil {
		errs = append(errs, err.Error())
	}
	if email != "" && !validator.Matches(email, validator.EmailRX) {
		errs = append(errs, fmt.Sprintf("invalid email %q", email))
	}
	if expires <= 0 {
		errs = append(errs, "-expires must be positive")
	}
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if len(errs) > 0 {
		fmt.Fprintln(stderr, strings.Join(errs, "\n"))
		return 2
	}

	db, err := openInviteDB(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()

	inv := rss.Invite{Note: strings.TrimSpace(note), ExpiresAt: time.Now().Add(expires)}
	if email != "" {
		// A bound invite's account starts confirmed, so only an address
		// confirmed on the waitlist can have one.
		e, err := psql.NewWaitlistRepository(db).GetByEmail(ctx, email)
		switch {
		case errors.Is(err, rss.ErrNoRecord) || (err == nil && e.ConfirmedAt.IsZero()):
			fmt.Fprintf(stderr, "no confirmed waitlist entry for %s; leave out -email for an open invite\n", email)
			return 1
		case err != nil:
			fmt.Fprintln(stderr, err)
			return 1
		}
		inv.Email, inv.WaitlistID = e.Email, e.ID
	}

	plain, hash, err := token.New()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := psql.NewInviteRepository(db).Create(ctx, inv, hash, cliActor); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s/signup?invite=%s\n", appURL, plain)
	return 0
}

// runInviteList runs "admin invite list": one line per invite, newest first.
func runInviteList(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var dsn string
	fs := flag.NewFlagSet("admin invite list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")
	if code := parseInviteFlags(fs, args, getenv, stderr); code >= 0 {
		return code
	}
	if dsn == "" {
		fmt.Fprintln(stderr, "database DSN must not be empty")
		return 2
	}

	db, err := openInviteDB(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()

	invites, err := allInvites(context.Background(), psql.NewInviteRepository(db))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	writeInvites(stdout, invites, time.Now())
	return 0
}

// invitePage is how many invites allInvites reads at a time.
const invitePage = 500

// allInvites reads every invite, newest first, a page at a time.
func allInvites(ctx context.Context, repo *psql.InviteRepository) ([]rss.Invite, error) {
	var all []rss.Invite
	before := ""
	for {
		page, err := repo.List(ctx, before, invitePage)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < invitePage {
			return all, nil
		}
		before = page[len(page)-1].ID
	}
}

// writeInvites writes invites as a table: ID, created, who it's for (the
// email, or the note), status, and expiry, in UTC.
func writeInvites(w io.Writer, invites []rss.Invite, now time.Time) {
	const layout = "2006-01-02 15:04"
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCREATED\tFOR\tSTATUS\tEXPIRES")
	for _, inv := range invites {
		who := inv.Email
		if who == "" {
			who = inv.Note
		}
		if who == "" {
			who = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", inv.ID, inv.CreatedAt.UTC().Format(layout),
			who, inv.Status(now), inv.ExpiresAt.UTC().Format(layout))
	}
	tw.Flush()
}

// runInviteRevoke runs "admin invite revoke <id>".
func runInviteRevoke(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var dsn string
	fs := flag.NewFlagSet("admin invite revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: admin invite revoke [-db-dsn <dsn>] <id>")
		fs.PrintDefaults()
	}
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")
	if code := parseInviteFlags(fs, args, getenv, stderr); code >= 0 {
		return code
	}

	var errs []string
	if dsn == "" {
		errs = append(errs, "database DSN must not be empty")
	}
	id := fs.Arg(0)
	if fs.NArg() != 1 || !validator.Matches(id, validator.UUIDRX) {
		errs = append(errs, "give the ID of one invite, as admin invite list shows it")
	}
	if len(errs) > 0 {
		fmt.Fprintln(stderr, strings.Join(errs, "\n"))
		return 2
	}

	db, err := openInviteDB(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer db.Close()

	err = psql.NewInviteRepository(db).Revoke(context.Background(), id, cliActor)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		fmt.Fprintf(stderr, "no invite with ID %s\n", id)
		return 1
	case errors.Is(err, rss.ErrInviteUnusable):
		fmt.Fprintf(stderr, "invite %s has already been used\n", id)
		return 1
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "revoked invite %s\n", id)
	return 0
}
