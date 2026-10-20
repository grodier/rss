// Package mail renders emails from embedded templates, queues them in the
// outbox (the emails table) and sends them from a background runner over SMTP,
// or logs them when there's no SMTP host.
package mail

import "context"

// Message is one email to one recipient.
type Message struct {
	To      string
	Subject string
	Text    string // plain-text body
	HTML    string // HTML body
}

// Sender delivers a message; satisfied by *SMTPSender and *LogSender.
type Sender interface {
	Send(ctx context.Context, m Message) error
}
