package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

	// now returns the current time. Tests override it.
	now func() time.Time
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
		now:            time.Now,
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

func (s *Server) Serve() error {
	shutdown := make(chan error)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit

		s.logger.Info("shutting down server", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := s.server.Shutdown(ctx)
		shutdown <- err
	}()

	s.logger.Info("starting server", "port", s.config.Port, "env", s.config.Env)

	err := s.server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdown
	if err != nil {
		return err
	}

	s.logger.Info("server stopped gracefully")

	return nil
}
