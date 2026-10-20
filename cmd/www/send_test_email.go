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
	cfg := defaultConfig().mail

	fs := flag.NewFlagSet("rss-www send-test-email", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: www send-test-email -to <address> [flags]")
		fs.PrintDefaults()
	}
	fs.StringVar(&to, "to", to, "Where to send the test email")
	registerMailFlags(fs, &cfg)

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
	// There's no -port or -env: links point at the app on its default port,
	// and production's requirements don't apply to a test send.
	cfg.setBaseURLDefault(defaultConfig().server.port)

	errs := cfg.validate("development")
	if to == "" {
		errs = append(errs, errors.New("-to must not be empty"))
	} else if _, err := netmail.ParseAddress(to); err != nil {
		errs = append(errs, fmt.Errorf("invalid -to %q: %v", to, err))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	templates, err := mail.ParseTemplates(ui.Emails, mail.App{URL: cfg.baseURL, SupportEmail: cfg.supportEmail})
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

	sender := newMailSender(cfg, newLogger(stdout, "development"))
	if err := sender.Send(context.Background(), msg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, logged := sender.(*mail.LogSender); !logged {
		fmt.Fprintf(stdout, "sent to %s\n", to)
	}
	return 0
}
