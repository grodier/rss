package server

import (
	"context"
	"errors"

	"github.com/grodier/rss/internal/accounts"
	"github.com/grodier/rss/internal/mail"
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
	return s.queueEmail(ctx, name, m)
}

// queueEmail queues m, the email name. Over the per-address cap it logs a
// warning and returns nil, so the page carries on.
func (s *Server) queueEmail(ctx context.Context, name string, m mail.Message) error {
	err := s.services.EmailQueue.Enqueue(ctx, m)
	if errors.Is(err, rss.ErrTooManyEmails) {
		s.logger.WarnContext(ctx, "email not queued: too many emails to this address", "email", name)
		return nil
	}
	return err
}

// sendLink stores link's token and queues its email.
func (s *Server) sendLink(ctx context.Context, link accounts.Link) error {
	if err := s.services.TokenService.Create(ctx, link.Token); err != nil {
		return err
	}
	return s.queueEmail(ctx, link.Name, link.Email)
}

// absoluteURL returns path, which must start with "/", on the app's base URL,
// for links in emails and shared timelines' links.
func (s *Server) absoluteURL(path string) string {
	return s.config.BaseURL + path
}
