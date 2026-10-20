package server

import (
	"context"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// sendEmail renders the named email (internal/ui/emails) with data and
// queues it to to. Over the per-address cap it logs a warning and returns
// nil, so the page carries on.
func (s *Server) sendEmail(ctx context.Context, to, name string, data any) error {
	m, err := s.emails.Render(name, data)
	if err != nil {
		return err
	}
	m.To = to

	err = s.services.EmailQueue.Enqueue(ctx, m)
	if errors.Is(err, rss.ErrTooManyEmails) {
		s.logger.WarnContext(ctx, "email not queued: too many emails to this address", "email", name)
		return nil
	}
	return err
}

// absoluteURL returns path, which must start with "/", on the app's base URL,
// for links in emails.
func (s *Server) absoluteURL(path string) string {
	return s.config.BaseURL + path
}
