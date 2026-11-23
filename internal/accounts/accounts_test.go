package accounts

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/ui"
)

func testEmails(t *testing.T) Emails {
	t.Helper()
	ts, err := mail.ParseTemplates(ui.Emails, mail.App{URL: "https://app.example", SupportEmail: "help@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return Emails{Templates: ts, AppURL: "https://app.example"}
}

func TestLinks(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	user := rss.User{ID: "u1", Name: "Ada", Email: "ada@example.com"}
	e := testEmails(t)

	tests := []struct {
		name    string
		make    func(rss.User, time.Time) (Link, error)
		purpose string
		path    string
		ttl     time.Duration
	}{
		{"confirmation", e.Confirmation, rss.TokenConfirmEmail, "/confirm-email", ConfirmLinkTTL},
		{"password reset", e.PasswordReset, rss.TokenResetPassword, "/password/reset", ResetLinkTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link, err := tt.make(user, now)
			if err != nil {
				t.Fatal(err)
			}
			want := rss.Token{UserID: user.ID, Purpose: tt.purpose, Email: user.Email, ExpiresAt: now.Add(tt.ttl)}
			got := link.Token
			got.Hash = nil
			if !got.ExpiresAt.Equal(want.ExpiresAt) || got.UserID != want.UserID || got.Purpose != want.Purpose || got.Email != want.Email {
				t.Errorf("token = %+v, want %+v", got, want)
			}
			if link.Email.To != user.Email || link.Name != tt.purpose {
				t.Errorf("email to %q named %q, want to %q named %q", link.Email.To, link.Name, user.Email, tt.purpose)
			}
			m := regexp.MustCompile(regexp.QuoteMeta("https://app.example"+tt.path+"?token=") + `([A-Za-z0-9_-]+)`).FindStringSubmatch(link.Email.Text)
			if m == nil {
				t.Fatalf("the email has no %s link:\n%s", tt.path, link.Email.Text)
			}
			if !bytes.Equal(token.Hash(m[1]), link.Token.Hash) {
				t.Error("the stored hash isn't the link's token's")
			}
			if !strings.Contains(link.Email.Text, "Ada") {
				t.Errorf("the email doesn't greet them by name:\n%s", link.Email.Text)
			}
		})
	}

	t.Run("each link is new", func(t *testing.T) {
		a, _ := e.Confirmation(user, now)
		b, _ := e.Confirmation(user, now)
		if bytes.Equal(a.Token.Hash, b.Token.Hash) {
			t.Error("two confirmations share a token")
		}
	})
}

func TestAccountDeleted(t *testing.T) {
	m, err := testEmails(t).AccountDeleted(rss.User{ID: "u1", Name: "Ada", Email: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != "ada@example.com" || m.Subject == "" || !strings.Contains(m.Text, "Ada") {
		t.Errorf("AccountDeleted = to %q, subject %q, text:\n%s", m.To, m.Subject, m.Text)
	}
}
