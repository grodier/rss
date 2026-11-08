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
}

// DatabasePinger checks that the database answers; *sql.DB is one.
type DatabasePinger interface {
	PingContext(ctx context.Context) error
}

type Services struct {
	Database DatabasePinger
}

type Server struct {
	config    Config
	server    *http.Server
	logger    *slog.Logger
	templates map[string]*template.Template
	services  Services

	// proxyHopsWarning logs, once, that clientIP took a non-public address
	// from X-Forwarded-For.
	proxyHopsWarning sync.Once
}

func New(logger *slog.Logger, cfg Config, services Services) (*Server, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		logger:    logger,
		config:    cfg,
		templates: templates,
		services:  services,
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
