package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	netmail "net/mail"

	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/ui"
)

// appDefaultPort is www's default -port, where the test email's links point
// without -base-url.
const appDefaultPort = 8080

// runSendTestEmail runs "admin send-test-email -to <address> [flags]": it
// renders the test email and sends it with the mail settings (www's mail
// flags), or logs it when there's no SMTP host, and returns the exit code. It
// doesn't open the database. Like runEnroll, it writes flag errors and the
// -help output to stderr.
func runSendTestEmail(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var to string
	cfg := cli.DefaultMail()

	fs := flag.NewFlagSet("admin send-test-email", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: admin send-test-email -to <address> [flags]")
		fs.PrintDefaults()
	}
	fs.StringVar(&to, "to", to, "Where to send the test email")
	cfg.RegisterFlags(fs)

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
	// There's no -port or -env: links point at the app (www) on its default
	// port, and production's requirements don't apply to a test send.
	cfg.SetBaseURLDefault(appDefaultPort)

	errs := cfg.Validate("development")
	if to == "" {
		errs = append(errs, errors.New("-to must not be empty"))
	} else if _, err := netmail.ParseAddress(to); err != nil {
		errs = append(errs, fmt.Errorf("invalid -to %q: %v", to, err))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	templates, err := mail.ParseTemplates(ui.Emails, mail.App{URL: cfg.BaseURL, SupportEmail: cfg.SupportEmail})
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

	sender := cfg.Sender(cli.NewLogger(stdout, "development"))
	if err := sender.Send(context.Background(), msg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, logged := sender.(*mail.LogSender); !logged {
		fmt.Fprintf(stdout, "sent to %s\n", to)
	}
	return 0
}
