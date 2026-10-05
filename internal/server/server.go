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
	Create(ctx context.Context, feed rss.Feed) (string, error)
	GetByID(ctx context.Context, id string) (rss.Feed, error)
	GetLatest(ctx context.Context) ([]rss.Feed, error)
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

var (
	_ FeedStore   = (*psql.FeedRepository)(nil)
	_ UserStore   = (*psql.UserRepository)(nil)
	_ SearchStore = (*psql.SearchRepository)(nil)
)

type Services struct {
	FeedService   FeedStore
	UserService   UserStore
	SearchService SearchStore
}

type Server struct {
	config      Config
	server      *http.Server
	logger      *slog.Logger
	templates   map[string]*template.Template
	formDecoder *form.Decoder

	sessionManager *scs.SessionManager
	services       Services
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
