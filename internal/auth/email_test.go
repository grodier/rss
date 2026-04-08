package auth

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggingEmailSender_SendVerificationEmail(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sender := NewLoggingEmailSender(logger)

	err := sender.SendVerificationEmail(context.Background(), "user@example.com", "test-token")
	if err != nil {
		t.Fatalf("SendVerificationEmail() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "verification email") {
		t.Errorf("expected log to contain 'verification email', got: %s", output)
	}
	if !strings.Contains(output, "user@example.com") {
		t.Errorf("expected log to contain recipient, got: %s", output)
	}
}

func TestLoggingEmailSender_SendPasswordResetEmail(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sender := NewLoggingEmailSender(logger)

	err := sender.SendPasswordResetEmail(context.Background(), "user@example.com", "reset-token")
	if err != nil {
		t.Fatalf("SendPasswordResetEmail() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "password reset email") {
		t.Errorf("expected log to contain 'password reset email', got: %s", output)
	}
	if !strings.Contains(output, "user@example.com") {
		t.Errorf("expected log to contain recipient, got: %s", output)
	}
}
