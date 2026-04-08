package auth

import "log/slog"

type Service struct {
	repo        Repository
	hasher      PasswordHasher
	emailSender EmailSender
	logger      *slog.Logger
}

func NewService(repo Repository, hasher PasswordHasher, emailSender EmailSender, logger *slog.Logger) *Service {
	return &Service{
		repo:        repo,
		hasher:      hasher,
		emailSender: emailSender,
		logger:      logger,
	}
}
