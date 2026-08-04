package server

import (
	"fmt"
	"log/slog"
	"net/http"
)

type Config struct {
	Port int
	Env  string
}

type Server struct {
	config Config
	server *http.Server
	logger *slog.Logger
}

func NewServer(logger *slog.Logger, cfg Config) *Server {
	return &Server{
		logger: logger,
		config: cfg,
		server: &http.Server{
			Addr:     fmt.Sprintf(":%d", cfg.Port),
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}
}

func (s *Server) Serve() error {
	return nil
}
