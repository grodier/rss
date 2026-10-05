package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/form/v4"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
)

type Config struct {
	Port int
	Env  string
}

// FeedStore is the feed persistence the server depends on.
type FeedStore interface {
	GetByID(ctx context.Context, id string) (rss.Feed, error)
	GetLatest(ctx context.Context) ([]rss.Feed, error)
	ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error)
}

// SiteStore is the site persistence the server depends on.
type SiteStore interface {
	GetByID(ctx context.Context, id string) (rss.Site, error)
}

// UserStore is the user persistence the server depends on.
type UserStore interface {
	Create(ctx context.Context, name, email, password string) (string, time.Time, error)
	Authenticate(ctx context.Context, email, password string) (string, error)
	Exists(ctx context.Context, id string) (bool, error)
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

var (
	_ FeedStore   = (*psql.FeedRepository)(nil)
	_ UserStore   = (*psql.UserRepository)(nil)
	_ SearchStore = (*psql.SearchRepository)(nil)
	_ SiteStore   = (*psql.SiteRepository)(nil)
	_ LookupStore = (*psql.LookupRepository)(nil)
)

type Services struct {
	FeedService   FeedStore
	UserService   UserStore
	SearchService SearchStore
	SiteService   SiteStore
	LookupService LookupStore
}

type Server struct {
	config      Config
	server      *http.Server
	logger      *slog.Logger
	templates   map[string]*template.Template
	formDecoder *form.Decoder

	sessionManager *scs.SessionManager
	services       Services

	// lookupWait is how long POST /lookups waits for a lookup to finish
	// before redirecting to its status page. Keep it well under WriteTimeout.
	lookupWait time.Duration

	// lookupLimiter caps distinct lookups per user (keyed by user ID).
	lookupLimiter *rateLimiter
}

func NewServer(logger *slog.Logger, cfg Config, services Services, sessionManager *scs.SessionManager) (*Server, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	s := &Server{
		logger:         logger,
		config:         cfg,
		templates:      templates,
		formDecoder:    form.NewDecoder(),
		services:       services,
		sessionManager: sessionManager,
		lookupWait:     3 * time.Second,
		lookupLimiter:  newRateLimiter(10, 10*time.Minute),
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
