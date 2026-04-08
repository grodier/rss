package auth

import (
	"context"
	"log/slog"
)

// EmailSender abstracts email delivery. Auth is the only consumer today;
// if other packages need to send email, extract this into its own package.
type EmailSender interface {
	SendVerificationEmail(ctx context.Context, to string, token string) error
	SendPasswordResetEmail(ctx context.Context, to string, token string) error
}

type LoggingEmailSender struct {
	logger *slog.Logger
}

func NewLoggingEmailSender(logger *slog.Logger) *LoggingEmailSender {
	return &LoggingEmailSender{logger: logger}
}

func (s *LoggingEmailSender) SendVerificationEmail(ctx context.Context, to string, token string) error {
	s.logger.Info("verification email",
		slog.String("to", to),
		slog.String("token", token),
	)
	return nil
}

func (s *LoggingEmailSender) SendPasswordResetEmail(ctx context.Context, to string, token string) error {
	s.logger.Info("password reset email",
		slog.String("to", to),
		slog.String("token", token),
	)
	return nil
}
