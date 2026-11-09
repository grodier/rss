package main

import (
	"bytes"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/admin"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

var testSecretKey = strings.Repeat("0f", 32)

// unreachableDSN can't connect, so a 2 from enroll shows its input was
// checked before it opened the database.
const unreachableDSN = "postgres://localhost:1/x?connect_timeout=1"

var secretRX = regexp.MustCompile(`secret=([A-Z2-7]+)`)

// codeReader answers enroll's prompt for a code: the current code for the
// secret in the URI enroll printed to out, or wrong when set.
type codeReader struct {
	out   *bytes.Buffer
	wrong bool
	sent  bool
}

func (r *codeReader) Read(p []byte) (int, error) {
	if r.sent && !r.wrong {
		return 0, io.EOF
	}
	r.sent = true
	code := "000000"
	if !r.wrong {
		m := secretRX.FindStringSubmatch(r.out.String())
		if m == nil {
			return 0, errors.New("no secret printed yet")
		}
		secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(m[1])
		if err != nil {
			return 0, err
		}
		code = admin.Code(secret, time.Now().Unix()/30)
	}
	return copy(p, code+"\n"), nil
}

// runEnrollTest runs admin enroll with args against the test database,
// answering with password and then a code.
func runEnrollTest(t *testing.T, password string, wrongCode bool, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	stdin := io.MultiReader(strings.NewReader(password+"\n"), &codeReader{out: &out, wrong: wrongCode})
	env := envFrom(map[string]string{"RSS_DB_DSN": psqltest.DSN(t), "RSS_SECRET_KEY": testSecretKey})
	code = run(append([]string{"enroll"}, args...), env, stdin, &out, &errOut)
	return code, out.String(), errOut.String()
}

var recoveryCodeRX = regexp.MustCompile(`(?m)^  [a-z2-7]{4}-[a-z2-7]{4}$`)

func TestEnroll(t *testing.T) {
	db := psqltest.NewDB(t)
	admins := psql.NewAdminRepository(db)
	email := fmt.Sprintf("enroll-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM admins WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup admin: %v", err)
		}
	})
	key, err := parseSecretKey(testSecretKey)
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runEnrollTest(t, "first long password", false, "-email", email)
	if code != 0 {
		t.Fatalf("enroll = %d; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"otpauth://totp/Paper%20Round%20Admin:", "\x1b[30;107m", "Enrolled " + email} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if n := len(recoveryCodeRX.FindAllString(stdout, -1)); n != 10 {
		t.Errorf("printed %d recovery codes, want 10:\n%s", n, stdout)
	}
	first, err := admins.Authenticate(t.Context(), email, "first long password")
	if err != nil {
		t.Fatalf("Authenticate after enroll: %v", err)
	}
	if _, err := admin.Open(key, first.TOTPSecret); err != nil {
		t.Errorf("the stored secret doesn't open with the key: %v", err)
	}
	// The confirming code was used.
	if first.TOTPLastStep == 0 {
		t.Error("totp_last_step = 0, want the first code's step")
	}

	t.Run("refuses a duplicate", func(t *testing.T) {
		code, _, stderr := runEnrollTest(t, "another long password", false, "-email", email)
		if code != 1 || !strings.Contains(stderr, "already exists; use -reset") {
			t.Errorf("enroll again = %d, stderr %q; want 1 and that the admin exists", code, stderr)
		}
	})

	t.Run("-reset replaces", func(t *testing.T) {
		code, stdout, stderr := runEnrollTest(t, "second long password", false, "-email", email, "-reset")
		if code != 0 {
			t.Fatalf("enroll -reset = %d; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "Reset "+email) || len(recoveryCodeRX.FindAllString(stdout, -1)) != 10 {
			t.Errorf("stdout:\n%s", stdout)
		}
		second, err := admins.Authenticate(t.Context(), email, "second long password")
		if err != nil {
			t.Fatalf("Authenticate with the new password: %v", err)
		}
		if second.ID != first.ID || bytes.Equal(second.TOTPSecret, first.TOTPSecret) {
			t.Error("-reset didn't keep the admin and replace its secret")
		}
		if _, err := admins.Authenticate(t.Context(), email, "first long password"); !errors.Is(err, rss.ErrWrongPassword) {
			t.Errorf("old password error = %v, want rss.ErrWrongPassword", err)
		}
	})

	t.Run("-reset needs an admin", func(t *testing.T) {
		code, _, stderr := runEnrollTest(t, "a long password", false, "-email", "nobody-"+email, "-reset")
		if code != 1 || !strings.Contains(stderr, "no admin with email") {
			t.Errorf("enroll -reset of no admin = %d, stderr %q", code, stderr)
		}
	})

	t.Run("wrong codes save nothing", func(t *testing.T) {
		other := "wrong-" + email
		code, stdout, stderr := runEnrollTest(t, "a long password", true, "-email", other)
		if code != 1 || !strings.Contains(stderr, "no code worked after 3 tries; nothing was saved") {
			t.Errorf("enroll with wrong codes = %d, stderr %q", code, stderr)
		}
		if n := strings.Count(stdout, "That code didn't work."); n != 3 {
			t.Errorf("said a code didn't work %d times, want 3", n)
		}
		if _, err := admins.GetByEmail(t.Context(), other); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByEmail after wrong codes error = %v, want rss.ErrNoRecord", err)
		}
	})
}

func TestEnrollInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		stdin string
		want  string
	}{
		// Checked before the database is opened.
		{"no DSN", []string{"-email", "a@example.com"}, map[string]string{"RSS_SECRET_KEY": testSecretKey}, "a long password\n", "database DSN must not be empty"},
		{"no key", []string{"-email", "a@example.com"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "a long password\n", "-secret-key (RSS_SECRET_KEY) must be set"},
		{"bad key", []string{"-email", "a@example.com", "-secret-key", "abc"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "a long password\n", "invalid -secret-key"},
		{"no email", nil, map[string]string{"RSS_DB_DSN": unreachableDSN, "RSS_SECRET_KEY": testSecretKey}, "a long password\n", "email: Email is required"},
		{"empty stdin", []string{"-email", "a@example.com"}, map[string]string{"RSS_DB_DSN": unreachableDSN, "RSS_SECRET_KEY": testSecretKey}, "", "password: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(append([]string{"enroll"}, tt.args...), envFrom(tt.env), strings.NewReader(tt.stdin), io.Discard, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("enroll = %d, stderr %q; want 2 and %q", code, stderr.String(), tt.want)
			}
		})
	}
}
