package server

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

func TestSendEmail(t *testing.T) {
	t.Run("renders the email and queues it to the address", func(t *testing.T) {
		queue := &fakeEmailQueue{}
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com"}, Services{EmailQueue: queue})

		if err := s.sendEmail(t.Context(), "someone@example.com", "test", nil); err != nil {
			t.Fatalf("sendEmail() error = %v, want nil", err)
		}

		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d messages, want 1", len(msgs))
		}
		m := msgs[0]
		if m.To != "someone@example.com" {
			t.Errorf("To = %q, want someone@example.com", m.To)
		}
		if m.Subject != "Test email from rss" {
			t.Errorf("Subject = %q, want %q", m.Subject, "Test email from rss")
		}
		if !strings.Contains(m.Text, "https://rss.example.com") || !strings.Contains(m.HTML, "https://rss.example.com") {
			t.Errorf("bodies don't link to the base URL:\ntext: %s\nhtml: %s", m.Text, m.HTML)
		}
	})

	t.Run("over the per-address cap logs a warning and returns nil", func(t *testing.T) {
		s := newTestServerWith(t, Services{EmailQueue: &fakeEmailQueue{err: fmt.Errorf("enqueue: %w", rss.ErrTooManyEmails)}})
		var buf bytes.Buffer
		s.logger = slog.New(slog.NewJSONHandler(&buf, nil))

		if err := s.sendEmail(t.Context(), "someone@example.com", "test", nil); err != nil {
			t.Fatalf("sendEmail() error = %v, want nil", err)
		}

		lines := logLines(t, &buf)
		if len(lines) != 1 || lines[0]["level"] != "WARN" || lines[0]["email"] != "test" {
			t.Fatalf("logs = %v, want one warning naming the email", lines)
		}
		if strings.Contains(buf.String(), "someone@example.com") {
			t.Errorf("log %q contains the address", buf.String())
		}
	})

	t.Run("another queue error is returned", func(t *testing.T) {
		boom := errors.New("boom")
		s := newTestServerWith(t, Services{EmailQueue: &fakeEmailQueue{err: boom}})

		if err := s.sendEmail(t.Context(), "someone@example.com", "test", nil); !errors.Is(err, boom) {
			t.Fatalf("sendEmail() error = %v, want %v", err, boom)
		}
	})

	t.Run("an unknown email is an error and queues nothing", func(t *testing.T) {
		queue := &fakeEmailQueue{}
		s := newTestServerWith(t, Services{EmailQueue: queue})

		if err := s.sendEmail(t.Context(), "someone@example.com", "no-such-email", nil); err == nil {
			t.Fatal("sendEmail() error = nil, want an error")
		}
		if n := len(queue.messages()); n != 0 {
			t.Errorf("queued %d messages, want 0", n)
		}
	})
}

func TestAbsoluteURL(t *testing.T) {
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com"}, Services{})

	// absoluteURL takes no request, so a request's Host can't leak into links.
	got := s.absoluteURL("/confirm?token=abc")
	if want := "https://rss.example.com/confirm?token=abc"; got != want {
		t.Errorf("absoluteURL() = %q, want %q", got, want)
	}
}
