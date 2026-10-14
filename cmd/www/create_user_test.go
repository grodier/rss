package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// unreachableDSN can't connect, so a 2 from runCreateUser shows validation
// ran before it opened the database.
const unreachableDSN = "postgres://localhost:1/x?connect_timeout=1"

func TestRunCreateUserRequiresDSN(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{"create-user", "-name", "Ada", "-email", "ada@example.com"}
	if code := run(args, noEnv, strings.NewReader("correct horse\n"), io.Discard, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if want := "database DSN must not be empty"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestRunCreateUserInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"missing name", []string{"-email", "ada@example.com"}, "correct horse\n", "name: Name is required"},
		{"invalid email", []string{"-name", "Ada", "-email", "not-an-email"}, "correct horse\n", "email: Email must be a valid email address"},
		{"empty stdin", []string{"-name", "Ada", "-email", "ada@example.com"}, "", "password: Password is required"},
		{"7-character password", []string{"-name", "Ada", "-email", "ada@example.com"}, "1234567\n", "password: Password must be at least 8 characters long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			args := append([]string{"create-user", "-db-dsn", unreachableDSN}, tt.args...)
			if code := run(args, noEnv, strings.NewReader(tt.stdin), io.Discard, &stderr); code != 2 {
				t.Fatalf("run() = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRunCreateUserReadsOneLine(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"pw with spaces \n", "pw with spaces "},
		{"pw\r\n", "pw"},
		{"pw", "pw"},
		{"a\nb\n", "a"},
	}

	for _, tt := range tests {
		got, err := readPassword(strings.NewReader(tt.in))
		if err != nil {
			t.Fatalf("readPassword(%q) error = %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("readPassword(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
