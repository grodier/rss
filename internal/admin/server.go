// Package admin is the admin site: owner-only tools for support, the
// waitlist and invites, feeds and queues, and observability (see "Admin" in
// docs/decisions.md). Its web pages, served by Server, and cmd/admin's
// subcommands share it, so each action is written once. It runs none of
// www's background workers.
package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/form/v4"
	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
)

type Config struct {
	Port int
	Env  string
	// Version identifies the build (the commit SHA in the container image);
	// /healthcheck reports it.
	Version string
	// BaseURL is the admin's own URL, without a trailing slash.
	BaseURL string
	// ProxyHops is how many reverse proxies in front of the admin append to
	// X-Forwarded-For. 0 ignores the header.
	ProxyHops int
	// SecretKey is the 32-byte key admins' TOTP secrets are sealed with
	// (Seal), held only in the admin service's environment.
	SecretKey []byte
	// AppURL is the reader's public URL, without a trailing slash: invites'
	// links point there.
	AppURL string
	// SupportEmail is where people can get help, shown in emails' footers.
	SupportEmail string
	// MailLimits are the email provider's sending limits, which invites are
	// checked against.
	MailLimits MailLimits
	// AppInternalURL is www's URL on the private network that the pinger
	// checks; "" when it doesn't run, which the service page says.
	AppInternalURL string
}

// DatabasePinger checks that the database answers; *sql.DB is one.
type DatabasePinger interface {
	PingContext(ctx context.Context) error
}

// AdminStore is the admin accounts' repository; *psql.AdminRepository is
// one.
type AdminStore interface {
	Authenticate(ctx context.Context, email, password string) (rss.Admin, error)
	Get(ctx context.Context, id string) (rss.Admin, error)
	GetByEmail(ctx context.Context, email string) (rss.Admin, error)
	Create(ctx context.Context, email string, c rss.AdminCredentials, by rss.AuditActor) (string, error)
	Reset(ctx context.Context, email string, c rss.AdminCredentials, by rss.AuditActor) error
	AcceptTOTPStep(ctx context.Context, id string, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, id string, hash []byte) (bool, error)
}

// AuditStore is the audit log's repository; *psql.AuditRepository is one.
type AuditStore interface {
	Record(ctx context.Context, e rss.AuditEntry) error
	List(ctx context.Context, f rss.AuditFilter, limit int) ([]rss.AuditEvent, error)
}

// WaitlistStore is the waitlist's repository; *psql.WaitlistRepository is
// one.
type WaitlistStore interface {
	Counts(ctx context.Context) (rss.WaitlistCounts, error)
	List(ctx context.Context, tab string, limit, offset int) ([]rss.WaitlistRow, error)
	ListWaiting(ctx context.Context, ids []string) ([]rss.WaitlistEntry, error)
	Oldest(ctx context.Context, n int) ([]rss.WaitlistEntry, error)
	GetByEmail(ctx context.Context, email string) (rss.WaitlistEntry, error)
}

// InviteStore is the invites' repository; *psql.InviteRepository is one.
type InviteStore interface {
	SendWave(ctx context.Context, w rss.Wave, available func(rss.EmailUsage) int, by rss.AuditActor) error
	SendInvite(ctx context.Context, inv rss.WaveInvite, note string, expiresAt time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) (string, error)
	Create(ctx context.Context, inv rss.Invite, tokenHash []byte, by rss.AuditActor) (rss.Invite, error)
	Get(ctx context.Context, id string) (rss.Invite, error)
	List(ctx context.Context, before string, limit int) ([]rss.Invite, error)
	Waves(ctx context.Context) ([]rss.InviteWave, error)
	Revoke(ctx context.Context, id string, by rss.AuditActor) error
	Resend(ctx context.Context, id string, inv rss.WaveInvite, expiresAt time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) error
}

// EmailStore is the outbox's repository; *psql.EmailRepository is one.
type EmailStore interface {
	Usage(ctx context.Context, now time.Time) (rss.EmailUsage, error)
}

// OverviewStore reads the Overview's counts; *psql.AdminOverviewRepository
// is one.
type OverviewStore interface {
	Overview(ctx context.Context, now time.Time) (rss.AdminOverview, error)
	Queues(ctx context.Context) ([]rss.QueueStatus, error)
	Jobs(ctx context.Context, since time.Time) (outcomes []rss.JobOutcomes, recorded bool, err error)
}

// WebPerfStore reads the real-user views for the web performance page;
// *psql.AdminWebPerfRepository is one.
type WebPerfStore interface {
	WebPerf(ctx context.Context, q rss.WebPerfQuery) (rss.WebPerf, error)
}

// AvailabilityReader reads the pinger's checks for the service page;
// *psql.AvailabilityRepository is one.
type AvailabilityReader interface {
	Uptime(ctx context.Context, now time.Time) (rss.Uptime, error)
	Incidents(ctx context.Context, since time.Time, limit int) ([]rss.Incident, error)
	LatestVersion(ctx context.Context) (rss.AvailabilityCheck, error)
}

// RuntimeReader reads www's runtime samples; *psql.MetricsRepository is
// one.
type RuntimeReader interface {
	ListRuntime(ctx context.Context, from, to time.Time) ([]metrics.RuntimeRow, error)
}

type Services struct {
	Database DatabasePinger
	Admins   AdminStore
	Audit    AuditStore
	Waitlist WaitlistStore
	Invites  InviteStore
	Emails   EmailStore
	Overview OverviewStore
	WebPerf  WebPerfStore

	Availability AvailabilityReader
	Runtime      RuntimeReader
}

type Server struct {
	config    Config
	server    *http.Server
	logger    *slog.Logger
	templates map[string]*template.Template
	// emails renders the emails the admin queues (invites), whose links
	// point at the reader, AppURL.
	emails   *mail.Templates
	services Services

	sessionManager *scs.SessionManager
	formDecoder    *form.Decoder
	// now is time.Now, except in tests.
	now func() time.Time

	// loginLimiter counts both login steps' attempts per client IP;
	// codeFailureLimiter counts wrong codes per admin.
	loginLimiter       *ratelimit.Limiter
	codeFailureLimiter *ratelimit.Limiter

	// proxyHopsWarning logs, once, that clientIP took a non-public address
	// from X-Forwarded-For.
	proxyHopsWarning sync.Once
}

// New returns the admin's server. sessionManager is NewSessionManager's.
func New(logger *slog.Logger, cfg Config, services Services, sessionManager *scs.SessionManager) (*Server, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	emails, err := mail.ParseTemplates(ui.Emails, mail.App{URL: cfg.AppURL, SupportEmail: cfg.SupportEmail})
	if err != nil {
		return nil, err
	}
	s := &Server{
		logger:    logger,
		config:    cfg,
		templates: templates,
		emails:    emails,
		services:  services,

		sessionManager:     sessionManager,
		formDecoder:        form.NewDecoder(),
		now:                time.Now,
		loginLimiter:       ratelimit.New(10, 15*time.Minute),
		codeFailureLimiter: ratelimit.New(5, 15*time.Minute),
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Port),
			ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
			IdleTimeout:  time.Minute,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
	}
	s.server.Handler = s.router()
	return s, nil
}

// Serve runs the HTTP server until ctx is canceled, then shuts it down
// gracefully (waiting up to 30s for in-flight requests). It returns early
// with the error if the server fails to start, e.g. the port is in use.
func (s *Server) Serve(ctx context.Context) error {
	s.logger.Info("starting admin server", "port", s.config.Port, "env", s.config.Env, "url", s.config.BaseURL)

	errc := make(chan error, 1)
	go func() { errc <- s.server.ListenAndServe() }()

	select {
	case err := <-errc:
		// Only Serve calls Shutdown, so this is a startup/listen failure.
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutting down admin server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// ListenAndServe returns ErrServerClosed as soon as Shutdown starts.
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	s.logger.Info("admin server stopped gracefully")
	return nil
}
