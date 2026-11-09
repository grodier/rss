package admin

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

func TestNewRecoveryCodes(t *testing.T) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("got %d codes and %d hashes, want 10 of each", len(codes), len(hashes))
	}
	shape := regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}$`)
	seen := map[string]bool{}
	for i, c := range codes {
		if !shape.MatchString(c) {
			t.Errorf("code %q isn't xxxx-xxxx in base32", c)
		}
		if seen[c] {
			t.Errorf("code %q repeats", c)
		}
		seen[c] = true
		if string(hashes[i]) != string(token.Hash(c)) {
			t.Errorf("hash %d isn't code %q's", i, c)
		}
		if got, ok := normalizeRecoveryCode(c); !ok || got != c {
			t.Errorf("normalizeRecoveryCode(%q) = %q, %v; want it unchanged", c, got, ok)
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"abcd-efgh", "abcd-efgh", true},
		{"ABCD-EFGH", "abcd-efgh", true},
		{"abcdefgh", "abcd-efgh", true},
		{"ab cd ef gh", "abcd-efgh", true},
		{"abcd-efg", "", false},
		{"abcd-efghi", "", false},
		{"abcd-efg1", "", false}, // 1 isn't base32
		{"123456", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeRecoveryCode(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("normalizeRecoveryCode(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestEnrollmentFinish(t *testing.T) {
	admins := newFakeAdmins()
	e, err := NewEnrollment("new@example.com", "a long password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.URI(), "otpauth://totp/Paper%20Round%20Admin:new%40example.com?secret=") {
		t.Errorf("URI = %q", e.URI())
	}
	step := testNow.Unix() / totpStepSeconds

	// A code from minutes away, outside the drift Verify allows.
	if _, err := e.Finish(t.Context(), admins, testKey, Code(e.secret, step+10), false, testNow); !errors.Is(err, ErrWrongCode) {
		t.Errorf("Finish with a wrong code error = %v, want ErrWrongCode", err)
	}
	if _, err := admins.GetByEmail(t.Context(), "new@example.com"); !errors.Is(err, rss.ErrNoRecord) {
		t.Fatalf("a wrong code stored the admin (GetByEmail error = %v)", err)
	}

	codes, err := e.Finish(t.Context(), admins, testKey, Code(e.secret, step), false, testNow)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(codes) != 10 {
		t.Errorf("got %d recovery codes, want 10", len(codes))
	}
	a, err := admins.Authenticate(t.Context(), "new@example.com", "a long password")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	secret, err := Open(testKey, a.TOTPSecret)
	if err != nil || string(secret) != string(e.secret) {
		t.Errorf("the stored secret doesn't open to the enrollment's: %v", err)
	}
	if a.TOTPLastStep != step {
		t.Errorf("TOTPLastStep = %d, want the first code's step %d, so it can't log in too", a.TOTPLastStep, step)
	}
	if ok, _ := admins.UseRecoveryCode(t.Context(), a.ID, token.Hash(codes[0])); !ok {
		t.Error("a recovery code doesn't work")
	}

	t.Run("a second enrollment of the email", func(t *testing.T) {
		e2, _ := NewEnrollment("new@example.com", "another long password")
		if _, err := e2.Finish(t.Context(), admins, testKey, Code(e2.secret, step), false, testNow); !errors.Is(err, rss.ErrDuplicateEmail) {
			t.Errorf("Finish error = %v, want rss.ErrDuplicateEmail", err)
		}
		if _, err := e2.Finish(t.Context(), admins, testKey, Code(e2.secret, step), true, testNow); err != nil {
			t.Fatalf("Finish with reset: %v", err)
		}
		if _, err := admins.Authenticate(t.Context(), "new@example.com", "another long password"); err != nil {
			t.Errorf("the reset password doesn't work: %v", err)
		}
	})

	t.Run("reset needs an admin", func(t *testing.T) {
		e3, _ := NewEnrollment("nobody@example.com", "a long password")
		if _, err := e3.Finish(t.Context(), admins, testKey, Code(e3.secret, step), true, testNow); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Finish error = %v, want rss.ErrNoRecord", err)
		}
	})
}
