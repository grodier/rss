package server

import (
	"log/slog"
	"net/http"
)

type Server struct {
	Port    int
	Env     string
	Version string

	server *http.Server
	logger *slog.Logger
}

func NewServer(logger *slog.Logger) *Server {
	s := &Server{
		logger: logger,
		server: &http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}

	return s
}

func (s *Server) Serve() error {
	return nil
}
