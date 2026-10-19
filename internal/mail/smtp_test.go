package mail

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func testMessage() Message {
	return Message{
		To:      "ada@example.com",
		Subject: "Confirm your email",
		Text:    "Hello Ada,\nopen https://rss.example.com/confirm?token=abc to confirm.\n",
		HTML:    "<p>Hello Ada,</p>\n<p><a href=\"https://rss.example.com/confirm?token=abc\">Confirm</a></p>\n",
	}
}

func mustParseAddress(t *testing.T, s string) *netmail.Address {
	t.Helper()
	a, err := netmail.ParseAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestBuildMessage(t *testing.T) {
	m := testMessage()
	from := mustParseAddress(t, "rss <hello@rss.example.com>")

	raw, to, err := buildMessage(from, "help@example.com", m)
	if err != nil {
		t.Fatalf("buildMessage() error = %v", err)
	}
	if to.Address != "ada@example.com" {
		t.Errorf("recipient = %q, want ada@example.com", to.Address)
	}

	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	h := msg.Header
	wantHeaders := map[string]string{
		"From":         `"rss" <hello@rss.example.com>`,
		"To":           "ada@example.com",
		"Reply-To":     "help@example.com",
		"Subject":      "Confirm your email",
		"Mime-Version": "1.0",
	}
	for key, want := range wantHeaders {
		if got := h.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, err := h.Date(); err != nil {
		t.Errorf("Date %q doesn't parse: %v", h.Get("Date"), err)
	}
	if id := h.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@rss.example.com>") {
		t.Errorf("Message-ID = %q, want <…@rss.example.com>", id)
	}

	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v), want multipart/alternative", h.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for _, want := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		part, err := mr.NextRawPart()
		if err != nil {
			t.Fatalf("NextRawPart() error = %v", err)
		}
		if got := part.Header.Get("Content-Type"); got != want.contentType {
			t.Errorf("part Content-Type = %q, want %q", got, want.contentType)
		}
		if got := part.Header.Get("Content-Transfer-Encoding"); got != "quoted-printable" {
			t.Errorf("part Content-Transfer-Encoding = %q, want quoted-printable", got)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		// Quoted-printable carries text line breaks as CRLF.
		if got := strings.ReplaceAll(string(body), "\r\n", "\n"); got != want.body {
			t.Errorf("%s part = %q, want %q", want.contentType, got, want.body)
		}
	}
	if _, err := mr.NextRawPart(); err != io.EOF {
		t.Errorf("after two parts: NextRawPart() error = %v, want io.EOF", err)
	}

	t.Run("non-ASCII subject", func(t *testing.T) {
		m := testMessage()
		m.Subject = "Réinitialiser ton mot de passe"
		raw, _, err := buildMessage(from, "", m)
		if err != nil {
			t.Fatalf("buildMessage() error = %v", err)
		}
		msg, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		encoded := msg.Header.Get("Subject")
		if !strings.HasPrefix(encoded, "=?utf-8?q?") {
			t.Errorf("Subject = %q, want it Q-encoded", encoded)
		}
		decoded, err := new(mime.WordDecoder).DecodeHeader(encoded)
		if err != nil || decoded != m.Subject {
			t.Errorf("decoded Subject = %q (%v), want %q", decoded, err, m.Subject)
		}
	})

	t.Run("no Reply-To", func(t *testing.T) {
		raw, _, err := buildMessage(from, "", testMessage())
		if err != nil {
			t.Fatalf("buildMessage() error = %v", err)
		}
		msg, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("ReadMessage() error = %v", err)
		}
		if _, ok := msg.Header["Reply-To"]; ok {
			t.Errorf("Reply-To = %q, want no header", msg.Header.Get("Reply-To"))
		}
	})
}

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	from := mustParseAddress(t, "hello@rss.example.com")
	tests := []struct {
		name   string
		mutate func(m *Message)
	}{
		{"LF in To", func(m *Message) { m.To = "ada@example.com\nBcc: eve@example.com" }},
		{"CR in To", func(m *Message) { m.To = "ada@example.com\rBcc: eve@example.com" }},
		{"LF in Subject", func(m *Message) { m.Subject = "Hi\nBcc: eve@example.com" }},
		{"CR in Subject", func(m *Message) { m.Subject = "Hi\rBcc: eve@example.com" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testMessage()
			tt.mutate(&m)
			if _, _, err := buildMessage(from, "", m); err == nil {
				t.Fatal("buildMessage() error = nil, want an error")
			}
		})
	}
}

// smtpSession is what fakeSMTPServer received.
type smtpSession struct {
	mailFrom, rcptTo string
	data             []byte
}

// fakeSMTPServer answers one minimal SMTP dialogue (no STARTTLS, no AUTH) on
// a listener on host, and returns its port and a channel that receives what
// it got.
func fakeSMTPServer(t *testing.T, host string) (int, <-chan smtpSession) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("listen on %s: %v", host, err)
	}
	t.Cleanup(func() { ln.Close() })

	got := make(chan smtpSession, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		tp := textproto.NewConn(conn)

		var s smtpSession
		tp.PrintfLine("220 localhost ESMTP fake")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			verb, _, _ := strings.Cut(line, " ")
			switch strings.ToUpper(verb) {
			case "EHLO", "HELO":
				tp.PrintfLine("250 localhost")
			case "MAIL":
				s.mailFrom = line
				tp.PrintfLine("250 OK")
			case "RCPT":
				s.rcptTo = line
				tp.PrintfLine("250 OK")
			case "DATA":
				tp.PrintfLine("354 Go ahead")
				s.data, err = tp.ReadDotBytes()
				if err != nil {
					return
				}
				tp.PrintfLine("250 OK")
			case "QUIT":
				tp.PrintfLine("221 Bye")
				got <- s
				return
			default:
				tp.PrintfLine("502 Not implemented")
			}
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestSMTPSenderSends(t *testing.T) {
	port, got := fakeSMTPServer(t, "127.0.0.1")
	s := &SMTPSender{
		Host:    "127.0.0.1",
		Port:    port,
		From:    "rss <hello@rss.example.com>",
		ReplyTo: "help@example.com",
		Timeout: 5 * time.Second,
	}

	m := testMessage()
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var session smtpSession
	select {
	case session = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the server didn't finish the dialogue")
	}
	if want := "MAIL FROM:<hello@rss.example.com>"; session.mailFrom != want {
		t.Errorf("MAIL = %q, want %q", session.mailFrom, want)
	}
	if want := "RCPT TO:<ada@example.com>"; session.rcptTo != want {
		t.Errorf("RCPT = %q, want %q", session.rcptTo, want)
	}
	msg, err := netmail.ReadMessage(bufio.NewReader(bytes.NewReader(session.data)))
	if err != nil {
		t.Fatalf("ReadMessage(data) error = %v", err)
	}
	if got := msg.Header.Get("Subject"); got != m.Subject {
		t.Errorf("Subject = %q, want %q", got, m.Subject)
	}
	if got := msg.Header.Get("To"); got != m.To {
		t.Errorf("To = %q, want %q", got, m.To)
	}
}

// silentServer accepts connections and never answers.
func silentServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		var conns []net.Conn
		for {
			conn, err := ln.Accept()
			if err != nil {
				// The listener was closed: the test is over.
				for _, c := range conns {
					c.Close()
				}
				return
			}
			conns = append(conns, conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSMTPSenderTimeout(t *testing.T) {
	s := &SMTPSender{
		Host:    "127.0.0.1",
		Port:    silentServer(t),
		From:    "hello@rss.example.com",
		Timeout: 200 * time.Millisecond,
	}

	start := time.Now()
	err := s.Send(context.Background(), testMessage())
	if err == nil {
		t.Fatal("Send() error = nil, want a timeout")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("Send() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Send() took %v, want about %v", elapsed, s.Timeout)
	}
}

func TestSMTPSenderContextCanceled(t *testing.T) {
	s := &SMTPSender{
		Host:    "127.0.0.1",
		Port:    silentServer(t),
		From:    "hello@rss.example.com",
		Timeout: time.Minute,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Send(ctx, testMessage())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Send() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Send() took %v, want it to stop when the context is done", elapsed)
	}
}

func TestSMTPSenderRequiresSTARTTLS(t *testing.T) {
	// 127.0.0.2 is this machine (on Linux) but not one of requireTLS's
	// exceptions, and fakeSMTPServer doesn't offer STARTTLS.
	port, _ := fakeSMTPServer(t, "127.0.0.2")
	s := &SMTPSender{Host: "127.0.0.2", Port: port, From: "hello@rss.example.com", Timeout: 5 * time.Second}
	err := s.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "doesn't offer STARTTLS") {
		t.Errorf("Send() error = %v, want it to refuse a server without STARTTLS", err)
	}
}

func TestRequireTLS(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", false},
		{"127.0.0.1", false},
		{"smtp.resend.com", true},
	}
	for _, tt := range tests {
		if got := requireTLS(tt.host); got != tt.want {
			t.Errorf("requireTLS(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
