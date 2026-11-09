package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRunSendTestEmail(t *testing.T) {
	t.Run("missing -to", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := run([]string{"send-test-email"}, noEnv, nil, io.Discard, &stderr); code != 2 {
			t.Fatalf("run() = %d, want 2", code)
		}
		if want := "-to must not be empty"; !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	})

	t.Run("invalid -to", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := run([]string{"send-test-email", "-to", "not-an-email"}, noEnv, nil, io.Discard, &stderr); code != 2 {
			t.Fatalf("run() = %d, want 2", code)
		}
		if want := `invalid -to "not-an-email"`; !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	})

	t.Run("invalid flags", func(t *testing.T) {
		var stderr bytes.Buffer
		args := []string{"send-test-email", "-to", "ada@example.com", "-base-url", "https://example.com/rss"}
		if code := run(args, noEnv, nil, io.Discard, &stderr); code != 2 {
			t.Fatalf("run() = %d, want 2", code)
		}
	})

	t.Run("no SMTP host logs the email", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		args := []string{"send-test-email", "-to", "ada@example.com", "-base-url", "https://rss.example.com"}
		if code := run(args, noEnv, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
		}
		for _, want := range []string{
			"email not sent (no SMTP host)",
			"to=ada@example.com",
			`subject="Test email from Paper Round"`,
			"This is a test email from Paper Round at https://rss.example.com.",
		} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
			}
		}
	})

	t.Run("send fails", func(t *testing.T) {
		var stderr bytes.Buffer
		// Nothing listens on port 1.
		args := []string{"send-test-email", "-to", "ada@example.com", "-smtp-host", "127.0.0.1", "-smtp-port", "1"}
		if code := run(args, noEnv, nil, io.Discard, &stderr); code != 1 {
			t.Fatalf("run() = %d, want 1; stderr = %q", code, stderr.String())
		}
	})
}
