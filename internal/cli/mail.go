package cli

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/grodier/rss/internal/mail"
)

// Mail is how the app sends email, and what emails link to: the mail flags,
// which www and admin's send-test-email share.
type Mail struct {
	// BaseURL is the app's public URL, without a trailing slash. Links in
	// emails are built from it, never from a request's Host header.
	BaseURL string
	// SupportEmail is where people can get help, and the Reply-To of emails.
	SupportEmail string
	// SMTPHost, when empty, makes the app log emails instead of sending them.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	From         string
}

// DefaultMailFrom is -mail-from's default, which production must override.
const DefaultMailFrom = "rss <hello@localhost>"

// DefaultMail returns the mail settings' defaults.
func DefaultMail() Mail {
	return Mail{SMTPPort: 587, From: DefaultMailFrom}
}

// RegisterFlags registers the mail flags in fs, writing to m. -base-url has
// no default here: call m.SetBaseURLDefault after fs.Parse.
func (m *Mail) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(&m.BaseURL, "base-url", "", "Public URL of the app, for links in emails and shared timelines (default http://localhost:<port>; required in production)")
	fs.StringVar(&m.SupportEmail, "support-email", m.SupportEmail, "Where people can get help: shown on pages and used as Reply-To (required in production)")
	fs.StringVar(&m.SMTPHost, "smtp-host", m.SMTPHost, "SMTP server (default: emails are logged, not sent; required in production)")
	fs.IntVar(&m.SMTPPort, "smtp-port", m.SMTPPort, "SMTP port: 465 for implicit TLS, otherwise STARTTLS")
	fs.StringVar(&m.SMTPUsername, "smtp-username", m.SMTPUsername, "SMTP username")
	fs.StringVar(&m.SMTPPassword, "smtp-password", m.SMTPPassword, "SMTP password")
	fs.StringVar(&m.From, "mail-from", m.From, "From address of every email (required in production)")
}

// SetBaseURLDefault defaults the base URL to the app on localhost at port,
// and trims a trailing slash.
func (m *Mail) SetBaseURLDefault(port int) {
	if m.BaseURL == "" {
		m.BaseURL = "http://localhost:" + strconv.Itoa(port)
	}
	m.BaseURL = strings.TrimSuffix(m.BaseURL, "/")
}

// Validate returns an error for each invalid mail setting, and in production
// for each missing one: emails carry one-time links, which must never end up
// in the logs there.
func (m Mail) Validate(env string) []error {
	var errs []error
	u, err := url.Parse(m.BaseURL)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must be an absolute http or https URL", m.BaseURL))
	case u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must not have a path, query or fragment", m.BaseURL))
	case env == "production" && u.Scheme != "https":
		errs = append(errs, errors.New("-base-url (RSS_BASE_URL) must be set to an https URL in production"))
	}
	if _, err := netmail.ParseAddress(m.From); err != nil {
		errs = append(errs, fmt.Errorf("invalid mail from %q: %v", m.From, err))
	}
	if m.SupportEmail != "" {
		if _, err := netmail.ParseAddress(m.SupportEmail); err != nil {
			errs = append(errs, fmt.Errorf("invalid support email %q: %v", m.SupportEmail, err))
		}
	}
	if m.SMTPPort < 1 || m.SMTPPort > 65535 {
		errs = append(errs, fmt.Errorf("invalid SMTP port %d: must be 1-65535", m.SMTPPort))
	}
	if env == "production" {
		if m.SMTPHost == "" {
			errs = append(errs, errors.New("-smtp-host (RSS_SMTP_HOST) must be set in production"))
		}
		if m.From == DefaultMailFrom {
			errs = append(errs, errors.New("-mail-from (RSS_MAIL_FROM) must be set in production"))
		}
		if m.SupportEmail == "" {
			errs = append(errs, errors.New("-support-email (RSS_SUPPORT_EMAIL) must be set in production"))
		}
	}
	return errs
}

// Sender returns the sender m describes: SMTP, or the log when there's no
// SMTP host.
func (m Mail) Sender(logger *slog.Logger) mail.Sender {
	if m.SMTPHost == "" {
		return &mail.LogSender{Logger: logger}
	}
	return &mail.SMTPSender{
		Host:     m.SMTPHost,
		Port:     m.SMTPPort,
		Username: m.SMTPUsername,
		Password: m.SMTPPassword,
		From:     m.From,
		ReplyTo:  m.SupportEmail,
	}
}
