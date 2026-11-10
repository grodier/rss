package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
)

func TestRunCreateUser(t *testing.T) {
	db := psqltest.NewDB(t)
	email := fmt.Sprintf("create-user-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	var stdout, stderr bytes.Buffer
	env := envFrom(map[string]string{"RSS_DB_DSN": psqltest.DSN(t)})
	args := []string{"create-user", "-name", "Ada", "-email", email}
	if code := run(args, env, strings.NewReader("correct horse battery\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	user, err := psql.NewUserRepository(db).GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got := cliAudit(t, db, "user", user.ID); !slices.Equal(got, []string{"user.create"}) {
		t.Errorf("audit log = %v, want user.create from the command line", got)
	}
}

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
		// The rules themselves are tested in internal/validator; these check
		// that flags and stdin reach them, before the database is opened.
		{"missing name", []string{"-email", "ada@example.com"}, "correct horse\n", "name: Name is required"},
		{"empty stdin", []string{"-name", "Ada", "-email", "ada@example.com"}, "", "password: Password is required"},
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

func TestReadLine(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"pw with spaces \n", "pw with spaces "},
		{"pw\r\n", "pw"},
		{"pw", "pw"},
		{"a\nb\n", "a"},
	}

	for _, tt := range tests {
		got, err := readLine(bufio.NewReader(strings.NewReader(tt.in)))
		if err != nil {
			t.Fatalf("readLine(%q) error = %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("readLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
