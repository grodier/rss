// Package admin is the admin site: owner-only tools for support, the
// waitlist and invites, feeds and queues, and observability (see "Admin" in
// docs/decisions.md). Its web pages, served by Server, and cmd/admin's
// subcommands share it, so each action is written once. It runs none of
// www's background workers.
package admin

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/form/v4"
	"github.com/grodier/rss/internal/accounts"
	"github.com/grodier/rss/internal/admin/render"
	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
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
	// AppURL is the reader's public URL, without a trailing slash: the links
	// in invites and account emails point there.
	AppURL string
	// SupportEmail is where people can get help, shown in emails' footers.
	SupportEmail string
	// NewsletterDomain is the domain of readers' newsletter addresses; the
	// invite form refuses addresses there. "" turns the check off.
	NewsletterDomain string
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
	SitesAnswers(ctx context.Context, ids []string) ([]string, error)
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

// EmailStore is the outbox's repository, for the emails the admin queues
// and the email page; *psql.EmailRepository is one.
type EmailStore interface {
	Usage(ctx context.Context, now time.Time) (rss.EmailUsage, error)
	Enqueue(ctx context.Context, m mail.Message) error
	List(ctx context.Context, q rss.AdminEmailQuery) ([]rss.AdminEmail, error)
	CountByStatus(ctx context.Context) (map[string]int, error)
	Retry(ctx context.Context, id string, by rss.AuditActor) error
	Cancel(ctx context.Context, id string, by rss.AuditActor) error
	QueueTest(ctx context.Context, m mail.Message, by rss.AuditActor) (string, error)
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
	WebPerfRoute(ctx context.Context, q rss.WebPerfQuery, route string) (rss.WebPerfRoute, error)
	// HasViews says whether route had a view from since on: whether it's
	// a page, for the endpoints pages' link to its web performance.
	HasViews(ctx context.Context, route string, since time.Time) (bool, error)
}

// EndpointsStore reads the request metrics and recent server errors for
// the endpoints pages; *psql.AdminEndpointsRepository is one.
type EndpointsStore interface {
	Endpoints(ctx context.Context, q rss.EndpointQuery) (rss.Endpoints, error)
	RouteErrors(ctx context.Context, route, method string, limit int) ([]rss.ServerError, error)
}

// AvailabilityReader reads the pinger's checks for the service page;
// *psql.AvailabilityRepository is one.
type AvailabilityReader interface {
	Uptime(ctx context.Context, now time.Time) (rss.Uptime, error)
	Incidents(ctx context.Context, since time.Time, limit int) ([]rss.Incident, error)
	LatestVersion(ctx context.Context) (rss.AvailabilityCheck, error)
	VersionsIn(ctx context.Context, spans []rss.Span) ([]string, error)
}

// RenderReader reads www's service from Render's API; *render.Client is
// one. Services.Render is nil when the admin has no Render settings, which
// the service page says.
type RenderReader interface {
	Instances(ctx context.Context, from, to time.Time, step time.Duration) (render.Instances, error)
	Deploys(ctx context.Context, limit int) ([]render.Deploy, error)
}

// FeedStore reads the feeds and sites pages and makes their fixes;
// *psql.AdminFeedRepository is one.
type FeedStore interface {
	ListFeeds(ctx context.Context, q rss.AdminFeedQuery) ([]rss.AdminFeedRow, error)
	CountFeeds(ctx context.Context, q string) (map[string]int, error)
	ListSites(ctx context.Context, q rss.AdminSiteQuery) ([]rss.AdminSiteRow, error)
	CountSites(ctx context.Context, q string) (map[string]int, error)
	Feed(ctx context.Context, id string) (rss.AdminFeed, error)
	Site(ctx context.Context, id string) (rss.AdminSite, error)
	SiteIcon(ctx context.Context, id string) (rss.SiteIcon, error)
	ResetBackoff(ctx context.Context, id string, by rss.AuditActor) error
	ClearGone(ctx context.Context, id string, by rss.AuditActor) error
	CheckIcon(ctx context.Context, id string, by rss.AuditActor) error
	CheckForFeeds(ctx context.Context, id string, by rss.AuditActor) (rss.Lookup, error)
	DiscoverSections(ctx context.Context) ([]rss.Section, error)
	SetDiscoverSection(ctx context.Context, id, sectionID string, by rss.AuditActor) error
	SeedList(ctx context.Context, q rss.AdminSeedQuery) ([]rss.AdminSeedEntry, error)
	CountSeed(ctx context.Context) (map[string]int, error)
	SeedOnList(ctx context.Context, siteKeys []string) (map[string]bool, error)
	AddSeedSites(ctx context.Context, lines []seed.Line, source string, by rss.AuditActor) (int, error)
	QueueSeedLookup(ctx context.Context, siteKey string, by rss.AuditActor) (rss.Lookup, error)
	RetrySeedLookups(ctx context.Context, by rss.AuditActor) (int, error)
	Catalog(ctx context.Context) ([]rss.AdminCatalogSection, error)
	PickSites(ctx context.Context, q rss.AdminPickQuery) ([]rss.AdminPickSite, error)
	CountPick(ctx context.Context) (map[string]int, error)
	SavePicks(ctx context.Context, picks []rss.DiscoverPick, skips []string, by rss.AuditActor) error
	Unskip(ctx context.Context, id string, by rss.AuditActor) error
}

// QueueStore reads the queues page and retries or deletes failed jobs;
// *psql.AdminQueuesRepository is one.
type QueueStore interface {
	Counts(ctx context.Context) ([]rss.AdminQueueCounts, error)
	JobsPerHour(ctx context.Context, since, now time.Time) ([]rss.JobsHour, error)
	Failures(ctx context.Context, queue string) ([]rss.QueueFailure, error)
	Retry(ctx context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error)
	Delete(ctx context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error)
}

// UserStore reads the users pages, account facts and counts, never a
// reader's reading, and makes a reader's page's actions;
// *psql.AdminUserRepository is one.
type UserStore interface {
	ListUsers(ctx context.Context, q rss.AdminUserQuery) ([]rss.AdminUserRow, error)
	User(ctx context.Context, id string) (rss.AdminUser, error)
	SendLink(ctx context.Context, t rss.Token, m mail.Message, by rss.AuditActor) error
	MarkConfirmed(ctx context.Context, id, email string, by rss.AuditActor) error
	LogOutEverywhere(ctx context.Context, id string, by rss.AuditActor) (int, error)
	SetFeature(ctx context.Context, id, feature string, on bool, by rss.AuditActor) error
	Delete(ctx context.Context, id string, by rss.AuditActor) (rss.User, error)
}

// Refresher fetches a feed now and records the outcome on it;
// *ingest.Refresher, with the admin's own fetch.Client, is one.
type Refresher interface {
	Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error)
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
	Users    UserStore

	Endpoints EndpointsStore

	Feeds     FeedStore
	Refresher Refresher
	Queues    QueueStore

	Availability AvailabilityReader
	Runtime      RuntimeReader
	Render       RenderReader
}

type Server struct {
	config    Config
	server    *http.Server
	logger    *slog.Logger
	templates map[string]*template.Template
	// emails renders the emails the admin queues (invites), whose links
	// point at the reader, AppURL.
	emails *mail.Templates
	// accountEmails makes the account emails www also sends (a reader's
	// page's actions), with links to the reader.
	accountEmails accounts.Emails
	services      Services

	sessionManager *scs.SessionManager
	formDecoder    *form.Decoder
	// now is time.Now, except in tests.
	now func() time.Time

	// loginLimiter counts both login steps' attempts per client IP;
	// codeFailureLimiter counts wrong codes per admin.
	loginLimiter       *ratelimit.Limiter
	codeFailureLimiter *ratelimit.Limiter

	// clientIPs finds requests' client addresses behind Config.ProxyHops
	// proxies (clientIP). Its Logger is the one New was given: a test that
	// replaces s.logger afterwards doesn't see its warning.
	clientIPs *httpserve.ClientIP
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
		logger:        logger,
		config:        cfg,
		templates:     templates,
		emails:        emails,
		accountEmails: accounts.Emails{Templates: emails, AppURL: cfg.AppURL},
		services:      services,

		sessionManager:     sessionManager,
		formDecoder:        form.NewDecoder(),
		now:                time.Now,
		loginLimiter:       ratelimit.New(10, 15*time.Minute),
		codeFailureLimiter: ratelimit.New(5, 15*time.Minute),
		clientIPs:          &httpserve.ClientIP{ProxyHops: cfg.ProxyHops, Logger: logger},
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
// gracefully (see httpserve.Serve).
func (s *Server) Serve(ctx context.Context) error {
	return httpserve.Serve(ctx, s.server, s.logger, "admin server", "port", s.config.Port, "env", s.config.Env, "url", s.config.BaseURL)
}
