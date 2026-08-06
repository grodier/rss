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
)

type Config struct {
	Port int
	Env  string
}

type Server struct {
	config    Config
	server    *http.Server
	logger    *slog.Logger
	templates map[string]*template.Template
}

func NewServer(logger *slog.Logger, cfg Config) (*Server, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	s := &Server{
		logger:    logger,
		config:    cfg,
		templates: templates,
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
