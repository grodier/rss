package mail

import (
	"context"
	"log/slog"
)

// LogSender logs emails instead of sending them, links included, so the flows
// can be followed without an email account. It's for development and CI:
// production refuses to start without an SMTP host.
type LogSender struct {
	Logger *slog.Logger
}

func (s *LogSender) Send(ctx context.Context, m Message) error {
	s.Logger.InfoContext(ctx, "email not sent (no SMTP host)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}
