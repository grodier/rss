package server

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
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
)

type Config struct {
	Port int
	Env  string
	// InviteCode, when set, turns on GET and POST /signup for people who have it.
	InviteCode string
	// Version identifies the build (the commit SHA in the container image);
	// /healthcheck reports it.
	Version string
	// ProxyHops is how many reverse proxies in front of the app append to
	// X-Forwarded-For. 0 ignores the header.
	ProxyHops int
	// BaseURL is the app's public URL, without a trailing slash. Links in
	// emails are built from it, never from a request's Host header.
	BaseURL string
	// SupportEmail is where people can get help; pages show it.
	SupportEmail string
}

// FeedStore is the feed persistence the server depends on.
type FeedStore interface {
	GetByID(ctx context.Context, id string) (rss.Feed, error)
	ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error)
	RequestRefresh(ctx context.Context, id string) error
}

// SiteStore is the site persistence the server depends on.
type SiteStore interface {
	GetByID(ctx context.Context, id string) (rss.Site, error)
	// GetIcon returns the site's stored icon, or rss.ErrNoRecord.
	GetIcon(ctx context.Context, siteID string) (rss.SiteIcon, error)
	// IconHashes returns the icon hash of each of siteIDs that has an icon.
	IconHashes(ctx context.Context, siteIDs []string) (map[string]string, error)
}

// UserStore is the user persistence the server depends on.
type UserStore interface {
	Create(ctx context.Context, name, email, password string) (string, time.Time, error)
	Authenticate(ctx context.Context, email, password string) (string, error)
	GetByEmail(ctx context.Context, email string) (rss.User, error)
	UpdateName(ctx context.Context, id, name string) error
	ChangePassword(ctx context.Context, userID, keepSessionID, current, next string) error
	ConfirmEmail(ctx context.Context, hash []byte) (string, error)
	ResetPassword(ctx context.Context, hash []byte, newPassword string) (rss.User, error)
	Delete(ctx context.Context, userID, password string) (rss.User, error)
	CheckPassword(ctx context.Context, userID, password string) error
	ChangeEmail(ctx context.Context, hash []byte) (old rss.User, newEmail string, err error)
}

// TokenStore stores the one-time tokens sent in email links (user_tokens
// rows).
type TokenStore interface {
	Create(ctx context.Context, t rss.Token) error
	Get(ctx context.Context, hash []byte, purpose string) (rss.Token, error)
}

// SessionStore stores logged-in devices (user_sessions rows).
type SessionStore interface {
	Create(ctx context.Context, userID string, idleBefore time.Time) (string, error)
	Get(ctx context.Context, id string) (rss.Session, error)
	Touch(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	DeleteOthers(ctx context.Context, userID, keepID string) (int64, error)
}

// SearchStore searches sites and feeds already in the database.
type SearchStore interface {
	Search(ctx context.Context, q string, limit int) ([]rss.SiteWithFeeds, error)
}

// LookupStore queues and reads site lookups.
type LookupStore interface {
	Request(ctx context.Context, siteKey, url string, doneTTL, failedTTL time.Duration) (rss.Lookup, error)
	GetByID(ctx context.Context, id string) (rss.Lookup, error)
	GetBySiteKey(ctx context.Context, siteKey string) (rss.Lookup, error)
}

// FeedRefresher fetches a feed and saves its articles; satisfied by
// *ingest.Refresher.
type FeedRefresher interface {
	Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error)
}

// ArticleStore reads saved articles.
type ArticleStore interface {
	GetByID(ctx context.Context, id string) (rss.Article, error)
	// ListByFeed returns a page of a feed's articles. Summary and Content
	// are left empty.
	ListByFeed(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error)
	// ListTimeline returns a page of the user's timeline. Summary and
	// Content are left empty.
	ListTimeline(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error)
}

// SubscriptionStore is the subscription persistence the server depends on.
type SubscriptionStore interface {
	Subscribe(ctx context.Context, userID, feedID string) error
	Unsubscribe(ctx context.Context, userID, feedID string) error
	SubscribedFeedIDs(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error)
	ListByUser(ctx context.Context, userID string) ([]rss.Subscription, error)
}

// ReadStore records and reports which articles a user has read.
type ReadStore interface {
	MarkRead(ctx context.Context, userID, articleID string) error
	ReadArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
}

// SaveStore records and lists which articles a user has saved.
type SaveStore interface {
	Save(ctx context.Context, userID, articleID string) error
	Unsave(ctx context.Context, userID, articleID string) error
	SavedArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
	// ListSaved returns a page of the user's saved articles matching q
	// (every one if q is ""), newest saved first. Summary and Content are
	// left empty.
	ListSaved(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error)
}

// ImportStore stores OPML imports and reads their results.
type ImportStore interface {
	Create(ctx context.Context, userID, filename string, feeds []rss.ImportFeed) (string, error)
	Get(ctx context.Context, id string) (rss.Import, []rss.ImportFeed, error)
	// ListByUser returns the user's newest limit imports, newest first.
	ListByUser(ctx context.Context, userID string, limit int) ([]rss.ImportSummary, error)
}

// EmailQueue queues emails for the mail runner to send; satisfied by
// *mail.Queue.
type EmailQueue interface {
	Enqueue(ctx context.Context, m mail.Message) error
}

var (
	_ ArticleStore      = (*psql.ArticleRepository)(nil)
	_ FeedRefresher     = (*ingest.Refresher)(nil)
	_ FeedStore         = (*psql.FeedRepository)(nil)
	_ UserStore         = (*psql.UserRepository)(nil)
	_ SessionStore      = (*psql.UserSessionRepository)(nil)
	_ TokenStore        = (*psql.TokenRepository)(nil)
	_ SearchStore       = (*psql.SearchRepository)(nil)
	_ SiteStore         = (*psql.SiteRepository)(nil)
	_ LookupStore       = (*psql.LookupRepository)(nil)
	_ SubscriptionStore = (*psql.SubscriptionRepository)(nil)
	_ ReadStore         = (*psql.ReadRepository)(nil)
	_ SaveStore         = (*psql.SaveRepository)(nil)
	_ ImportStore       = (*psql.ImportRepository)(nil)
	_ EmailQueue        = (*mail.Queue)(nil)
)

type Services struct {
	FeedService         FeedStore
	UserService         UserStore
	SessionService      SessionStore
	TokenService        TokenStore
	SearchService       SearchStore
	SiteService         SiteStore
	LookupService       LookupStore
	Refresher           FeedRefresher
	ArticleService      ArticleStore
	SubscriptionService SubscriptionStore
	ReadService         ReadStore
	SaveService         SaveStore
	ImportService       ImportStore
	EmailQueue          EmailQueue
}

type Server struct {
	config      Config
	server      *http.Server
	logger      *slog.Logger
	templates   map[string]*template.Template
	emails      *mail.Templates
	formDecoder *form.Decoder

	sessionManager *scs.SessionManager
	services       Services

	// lookupWait is how long POST /lookups waits for a lookup to finish
	// before redirecting to its status page. Keep it well under WriteTimeout.
	lookupWait time.Duration

	// lookupLimiter caps distinct lookups per user (keyed by user ID).
	lookupLimiter *rateLimiter

	// importLimiter caps OPML imports per user (keyed by user ID).
	importLimiter *rateLimiter

	// refreshLimiter caps feed refreshes per user (keyed by user ID).
	refreshLimiter *rateLimiter

	// loginLimiter caps login attempts per client IP (keyed by ipRateLimitKey).
	loginLimiter *rateLimiter

	// loginFailureLimiter caps failed logins per submitted email (lowercased).
	loginFailureLimiter *rateLimiter

	// signupLimiter caps signup attempts per client IP (keyed by ipRateLimitKey).
	signupLimiter *rateLimiter

	// passwordChangeLimiter caps password change attempts per user (keyed by
	// user ID).
	passwordChangeLimiter *rateLimiter

	// confirmResendLimiter caps confirmation emails sent with Resend per user
	// (keyed by user ID).
	confirmResendLimiter *rateLimiter

	// resetIPLimiter caps password reset requests per client IP (keyed by
	// ipRateLimitKey).
	resetIPLimiter *rateLimiter

	// resetEmailLimiter caps password reset requests per submitted email
	// (lowercased).
	resetEmailLimiter *rateLimiter

	// deleteAccountLimiter caps account deletion attempts per user (keyed by
	// user ID).
	deleteAccountLimiter *rateLimiter

	// emailChangeLimiter caps email change requests per user (keyed by user
	// ID).
	emailChangeLimiter *rateLimiter

	// proxyHopsWarning logs, once, that clientIP took a non-public address
	// from X-Forwarded-For.
	proxyHopsWarning sync.Once
}

func NewServer(logger *slog.Logger, cfg Config, services Services, sessionManager *scs.SessionManager) (*Server, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	emails, err := mail.ParseTemplates(ui.Emails, mail.App{URL: cfg.BaseURL, SupportEmail: cfg.SupportEmail})
	if err != nil {
		return nil, err
	}

	s := &Server{
		logger:                logger,
		config:                cfg,
		templates:             templates,
		emails:                emails,
		formDecoder:           form.NewDecoder(),
		services:              services,
		sessionManager:        sessionManager,
		lookupWait:            3 * time.Second,
		lookupLimiter:         newRateLimiter(10, 10*time.Minute),
		importLimiter:         newRateLimiter(5, time.Hour),
		refreshLimiter:        newRateLimiter(10, 10*time.Minute),
		loginLimiter:          newRateLimiter(20, 10*time.Minute),
		loginFailureLimiter:   newRateLimiter(10, 15*time.Minute),
		signupLimiter:         newRateLimiter(10, time.Hour),
		passwordChangeLimiter: newRateLimiter(10, 10*time.Minute),
		confirmResendLimiter:  newRateLimiter(3, time.Hour),
		resetIPLimiter:        newRateLimiter(10, time.Hour),
		resetEmailLimiter:     newRateLimiter(3, time.Hour),
		deleteAccountLimiter:  newRateLimiter(10, 10*time.Minute),
		emailChangeLimiter:    newRateLimiter(5, time.Hour),
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
	s.logger.Info("starting server", "port", s.config.Port, "env", s.config.Env)

	errc := make(chan error, 1)
	go func() { errc <- s.server.ListenAndServe() }()

	select {
	case err := <-errc:
		// Only Serve calls Shutdown, so this is a startup/listen failure.
		return err
	case <-ctx.Done():
	}

	s.logger.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// ListenAndServe returns ErrServerClosed as soon as Shutdown starts.
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	s.logger.Info("server stopped gracefully")
	return nil
}
