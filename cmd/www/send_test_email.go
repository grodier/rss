package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	netmail "net/mail"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/ui"
)

// runSendTestEmail runs "www send-test-email -to <address> [flags]": it
// renders the test email and sends it with the mail settings, or logs it when
// there's no SMTP host, and returns the exit code. It doesn't open the
// database. Like runMigrate, it writes flag errors and the -help output to
// stderr.
func runSendTestEmail(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var to string
	cfg := defaultConfig()

	fs := flag.NewFlagSet("rss-www send-test-email", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: www send-test-email -to <address> [flags]")
		fs.PrintDefaults()
	}
	fs.StringVar(&to, "to", to, "Where to send the test email")
	registerMailFlags(fs, &cfg.mail)

	if err := setFlagsFromEnv(fs, getenv); err != nil {
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
	cfg.mail.setBaseURLDefault(cfg.server.port)

	errs := cfg.mail.validate(cfg.env)
	if to == "" {
		errs = append(errs, errors.New("-to must not be empty"))
	} else if _, err := netmail.ParseAddress(to); err != nil {
		errs = append(errs, fmt.Errorf("invalid -to %q: %v", to, err))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	templates, err := mail.ParseTemplates(ui.Emails, mail.App{URL: cfg.mail.baseURL, SupportEmail: cfg.mail.supportEmail})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	msg, err := templates.Render("test", nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	msg.To = to

	sender := newMailSender(cfg.mail, newLogger(stdout, cfg.env))
	if err := sender.Send(context.Background(), msg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, logged := sender.(*mail.LogSender); !logged {
		fmt.Fprintf(stdout, "sent to %s\n", to)
	}
	return 0
}
