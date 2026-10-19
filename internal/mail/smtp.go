package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// defaultTimeout bounds a send when SMTPSender.Timeout is zero.
const defaultTimeout = 10 * time.Second

// SMTPSender sends email through an SMTP server: over implicit TLS on port
// 465, otherwise over STARTTLS, which only a local server (Mailpit) may skip.
type SMTPSender struct {
	Host     string
	Port     int
	Username string // when set, authenticates with PLAIN
	Password string
	From     string // the From header; its address is also the envelope sender
	ReplyTo  string // the Reply-To header, omitted when empty
	// Timeout bounds the dial and the whole exchange; 0 means 10 s.
	Timeout time.Duration
}

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	from, err := netmail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid From address %q: %w", s.From, err)
	}
	msg, to, err := buildMessage(from, s.ReplyTo, m)
	if err != nil {
		return err
	}

	timeout := s.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)

	if err := s.send(ctx, deadline, from.Address, to.Address, msg); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("send email: %w", ctx.Err())
		}
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

func (s *SMTPSender) send(ctx context.Context, deadline time.Time, from, to string, msg []byte) error {
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{ServerName: s.Host}
	var conn net.Conn
	var err error
	if s.Port == 465 {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(dialCtx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return err
	}
	// Canceling ctx closes the connection, failing whatever call is blocked.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return err
			}
		} else if requireTLS(s.Host) {
			return fmt.Errorf("%s doesn't offer STARTTLS", s.Host)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// requireTLS reports whether a connection to host must be encrypted: always,
// except to a server on this machine (Mailpit in development).
func requireTLS(host string) bool {
	return host != "localhost" && host != "127.0.0.1"
}

// buildMessage returns m as a multipart/alternative message from from, with a
// plain-text and an HTML part, and the recipient's parsed address. replyTo is
// omitted when empty.
func buildMessage(from *netmail.Address, replyTo string, m Message) ([]byte, *netmail.Address, error) {
	if strings.ContainsAny(m.To, "\r\n") {
		return nil, nil, errors.New("recipient contains a line break")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, nil, errors.New("subject contains a line break")
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid recipient %q: %w", m.To, err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ contentType, content string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, nil, err
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(part.content)); err != nil {
			return nil, nil, err
		}
		if err := qw.Close(); err != nil {
			return nil, nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, nil, err
	}

	id := make([]byte, 16)
	rand.Read(id)
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var msg bytes.Buffer
	header := func(key, value string) {
		fmt.Fprintf(&msg, "%s: %s\r\n", key, value)
	}
	header("From", formatAddress(from))
	header("To", formatAddress(to))
	if replyTo != "" {
		addr, err := netmail.ParseAddress(replyTo)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid Reply-To address %q: %w", replyTo, err)
		}
		header("Reply-To", formatAddress(addr))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()}))
	msg.WriteString("\r\n")
	msg.Write(body.Bytes())
	return msg.Bytes(), to, nil
}

// formatAddress formats a for a header: the bare address when it has no name,
// otherwise the name (encoded if it isn't ASCII) and the address in <>.
func formatAddress(a *netmail.Address) string {
	if a.Name == "" {
		return a.Address
	}
	return a.String()
}
