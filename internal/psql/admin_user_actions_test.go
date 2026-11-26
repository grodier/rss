package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// actionTestUser inserts a user with a unique address, confirmed at
// confirmedAt (nil for not), deleting it and the emails to it (and their
// daily counts) on cleanup, and returns its ID and address.
func actionTestUser(t *testing.T, db *sql.DB, confirmedAt any) (string, string) {
	t.Helper()
	email := fmt.Sprintf("action-%d@example.com", psqltest.Unique())
	t.Cleanup(func() { deleteEmailsTo(t, db, email) })
	return adminTestUser(t, db, "Action User", email, time.Now().Add(-time.Hour), confirmedAt), email
}

// newLinkToken returns a new token for userID's link with purpose to email.
func newLinkToken(t *testing.T, userID, purpose, email string) rss.Token {
	t.Helper()
	_, hash, err := token.New()
	if err != nil {
		t.Fatal(err)
	}
	return rss.Token{Hash: hash, UserID: userID, Purpose: purpose, Email: email, ExpiresAt: time.Now().Add(time.Hour)}
}

// countRows runs a count query.
func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// onlyEntry returns by's one audit entry, failing unless there's exactly
// one, with action on user id.
func onlyEntry(t *testing.T, db *sql.DB, by rss.AuditActor, action, id string) rss.AuditEvent {
	t.Helper()
	got := auditBy(t, db, by)
	if len(got) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(got))
	}
	if e := got[0]; e.Action != action || e.TargetType != "user" || e.TargetID != id {
		t.Fatalf("audit entry = %s on %s %s, want %s on user %s", e.Action, e.TargetType, e.TargetID, action, id)
	}
	return got[0]
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositorySendLink(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)

	for _, tt := range []struct{ purpose, action string }{
		{rss.TokenConfirmEmail, "user.resend_confirmation"},
		{rss.TokenResetPassword, "user.send_password_reset"},
	} {
		t.Run(tt.purpose, func(t *testing.T) {
			id, email := actionTestUser(t, db, nil)
			expired := newLinkToken(t, id, rss.TokenResetPassword, email)
			if _, err := db.Exec(`INSERT INTO user_tokens (hash, user_id, purpose, email, expires_at) VALUES ($1, $2, $3, $4, now() - interval '1 minute')`,
				expired.Hash, id, expired.Purpose, email); err != nil {
				t.Fatal(err)
			}
			by := testActor(t, db)
			tok := newLinkToken(t, id, tt.purpose, email)
			if err := repo.SendLink(t.Context(), tok, mail.Message{To: email, Subject: "Your link", Text: "t", HTML: "h"}, by); err != nil {
				t.Fatalf("SendLink: %v", err)
			}
			if n := countRows(t, db, `SELECT count(*) FROM user_tokens WHERE user_id = $1 AND hash = $2 AND purpose = $3 AND email = $4`, id, tok.Hash, tt.purpose, email); n != 1 {
				t.Errorf("the link's tokens = %d, want 1", n)
			}
			if n := countRows(t, db, `SELECT count(*) FROM user_tokens WHERE hash = $1`, expired.Hash); n != 0 {
				t.Error("the expired link is still stored")
			}
			if n := countRows(t, db, `SELECT count(*) FROM emails WHERE to_address = $1 AND subject = 'Your link' AND status = 'pending'`, email); n != 1 {
				t.Errorf("emails queued = %d, want 1", n)
			}
			e := onlyEntry(t, db, by, tt.action, id)
			if e.After["email_to"] != email || e.After["link_expires_at"] == nil || e.Before != nil {
				t.Errorf("audit before %v, after %v; want the address and the expiry after", e.Before, e.After)
			}
		})
	}

	// refused checks that SendLink returns want and stores, queues and
	// records nothing.
	refused := func(t *testing.T, tok rss.Token, to string, want error) {
		t.Helper()
		by := testActor(t, db)
		err := repo.SendLink(t.Context(), tok, mail.Message{To: to, Subject: "s", Text: "t", HTML: "h"}, by)
		if !errors.Is(err, want) {
			t.Fatalf("SendLink = %v, want %v", err, want)
		}
		if n := countRows(t, db, `SELECT count(*) FROM user_tokens WHERE hash = $1`, tok.Hash); n != 0 {
			t.Error("the token was stored")
		}
		if n := countRows(t, db, `SELECT count(*) FROM emails WHERE to_address = $1 AND subject = 's'`, to); n != 0 {
			t.Error("the email was queued")
		}
		if got := auditBy(t, db, by); len(got) != 0 {
			t.Errorf("recorded %d entries, want none", len(got))
		}
	}

	t.Run("a confirmation to a confirmed address", func(t *testing.T) {
		id, email := actionTestUser(t, db, time.Now())
		refused(t, newLinkToken(t, id, rss.TokenConfirmEmail, email), email, rss.ErrAlreadyConfirmed)
	})
	t.Run("a reset to a confirmed address is sent", func(t *testing.T) {
		id, email := actionTestUser(t, db, time.Now())
		if err := repo.SendLink(t.Context(), newLinkToken(t, id, rss.TokenResetPassword, email), mail.Message{To: email, Subject: "s", Text: "t", HTML: "h"}, testActor(t, db)); err != nil {
			t.Fatalf("SendLink: %v", err)
		}
	})
	t.Run("an address that changed", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		old := "old-" + email
		t.Cleanup(func() { deleteEmailsTo(t, db, old) })
		refused(t, newLinkToken(t, id, rss.TokenResetPassword, old), old, rss.ErrAddressChanged)
	})
	t.Run("the address in another case", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		upper := strings.ToUpper(email)
		if err := repo.SendLink(t.Context(), newLinkToken(t, id, rss.TokenResetPassword, upper), mail.Message{To: upper, Subject: "s", Text: "t", HTML: "h"}, testActor(t, db)); err != nil {
			t.Fatalf("SendLink: %v", err)
		}
	})
	t.Run("an unknown user", func(t *testing.T) {
		refused(t, newLinkToken(t, "00000000-0000-4000-8000-000000000000", rss.TokenResetPassword, "nobody@example.com"), "nobody@example.com", rss.ErrNoRecord)
	})
	t.Run("over the per-address cap", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		for range maxEmailsPerAddressPerHour {
			if _, err := enqueue(t.Context(), db, mail.Message{To: email, Subject: "earlier"}); err != nil {
				t.Fatal(err)
			}
		}
		refused(t, newLinkToken(t, id, rss.TokenResetPassword, email), email, rss.ErrTooManyEmails)
	})
	t.Run("a link the admin doesn't send", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		if err := repo.SendLink(t.Context(), newLinkToken(t, id, rss.TokenChangeEmail, email), mail.Message{To: email}, testActor(t, db)); err == nil {
			t.Error("SendLink of a change_email link worked")
		}
	})
	t.Run("not recorded, not sent", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		tok := newLinkToken(t, id, rss.TokenResetPassword, email)
		if err := repo.SendLink(t.Context(), tok, mail.Message{To: email, Subject: "s", Text: "t", HTML: "h"}, brokenActor(t, db)); err == nil {
			t.Fatal("SendLink with a broken actor worked")
		}
		if n := countRows(t, db, `SELECT count(*) FROM emails WHERE to_address = $1`, email); n != 0 {
			t.Error("the email was queued without its record")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositoryMarkConfirmed(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)
	tokens := NewTokenRepository(db)

	t.Run("unconfirmed", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		confirm := createToken(t, tokens, id, rss.TokenConfirmEmail, email, time.Now().Add(time.Hour))
		reset := createToken(t, tokens, id, rss.TokenResetPassword, email, time.Now().Add(time.Hour))
		by := testActor(t, db)

		// The address as the page showed it, in any case.
		if err := repo.MarkConfirmed(t.Context(), id, strings.ToUpper(email), by); err != nil {
			t.Fatalf("MarkConfirmed: %v", err)
		}
		var stored sql.NullTime
		if err := db.QueryRow(`SELECT email_confirmed_at FROM users WHERE id = $1`, id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !stored.Valid || time.Since(stored.Time) > time.Minute {
			t.Errorf("email_confirmed_at = %v, want now", stored)
		}
		if tokenExists(t, tokens, confirm) {
			t.Error("the confirmation link still works")
		}
		if !tokenExists(t, tokens, reset) {
			t.Error("the reset link was deleted too")
		}
		e := onlyEntry(t, db, by, "user.mark_confirmed", id)
		if v, ok := e.Before["email_confirmed_at"]; !ok || v != nil || e.After["email_confirmed_at"] == nil || e.After["email"] != email {
			t.Errorf("audit before %v, after %v", e.Before, e.After)
		}
	})

	t.Run("already confirmed", func(t *testing.T) {
		confirmedAt := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		id, email := actionTestUser(t, db, confirmedAt)
		by := testActor(t, db)
		if err := repo.MarkConfirmed(t.Context(), id, email, by); !errors.Is(err, rss.ErrAlreadyConfirmed) {
			t.Fatalf("MarkConfirmed = %v, want %v", err, rss.ErrAlreadyConfirmed)
		}
		var stored time.Time
		if err := db.QueryRow(`SELECT email_confirmed_at FROM users WHERE id = $1`, id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !stored.Equal(confirmedAt) {
			t.Errorf("email_confirmed_at = %v, want it left at %v", stored, confirmedAt)
		}
		if got := auditBy(t, db, by); len(got) != 0 {
			t.Errorf("recorded %d entries, want none", len(got))
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		if err := repo.MarkConfirmed(t.Context(), "00000000-0000-4000-8000-000000000000", "nobody@example.com", testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("MarkConfirmed = %v, want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("an address that changed", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		by := testActor(t, db)
		if err := repo.MarkConfirmed(t.Context(), id, "old-"+email, by); !errors.Is(err, rss.ErrAddressChanged) {
			t.Fatalf("MarkConfirmed = %v, want %v", err, rss.ErrAddressChanged)
		}
		if n := countRows(t, db, `SELECT count(*) FROM users WHERE id = $1 AND email_confirmed_at IS NOT NULL`, id); n != 0 {
			t.Error("confirmed an address the admin didn't see")
		}
		if got := auditBy(t, db, by); len(got) != 0 {
			t.Errorf("recorded %d entries, want none", len(got))
		}
	})

	t.Run("not recorded, not confirmed", func(t *testing.T) {
		id, email := actionTestUser(t, db, nil)
		if err := repo.MarkConfirmed(t.Context(), id, email, brokenActor(t, db)); err == nil {
			t.Fatal("MarkConfirmed with a broken actor worked")
		}
		if n := countRows(t, db, `SELECT count(*) FROM users WHERE id = $1 AND email_confirmed_at IS NOT NULL`, id); n != 0 {
			t.Error("confirmed without its record")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositoryLogOutEverywhere(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)

	sessions := func(t *testing.T, id string, seen ...time.Time) {
		t.Helper()
		for _, at := range seen {
			if _, err := db.Exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, $2)`, id, at); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("logs out every device", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		other, _ := actionTestUser(t, db, nil)
		now := time.Now()
		// Two devices, and one past the idle timeout, which isn't one.
		sessions(t, id, now.Add(-time.Minute), now.Add(-24*time.Hour), now.Add(-rss.SessionIdleTimeout-time.Hour))
		sessions(t, other, now)
		by := testActor(t, db)

		n, err := repo.LogOutEverywhere(t.Context(), id, by)
		if err != nil {
			t.Fatalf("LogOutEverywhere: %v", err)
		}
		if n != 2 {
			t.Errorf("devices = %d, want 2", n)
		}
		if left := countRows(t, db, `SELECT count(*) FROM user_sessions WHERE user_id = $1`, id); left != 0 {
			t.Errorf("%d sessions left, want 0", left)
		}
		if left := countRows(t, db, `SELECT count(*) FROM user_sessions WHERE user_id = $1`, other); left != 1 {
			t.Errorf("the other user's sessions = %d, want 1", left)
		}
		e := onlyEntry(t, db, by, "user.log_out_everywhere", id)
		if e.Before["devices"] != float64(2) || e.After["devices"] != float64(0) {
			t.Errorf("audit before %v, after %v", e.Before, e.After)
		}
	})

	t.Run("no devices", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		by := testActor(t, db)
		if n, err := repo.LogOutEverywhere(t.Context(), id, by); err != nil || n != 0 {
			t.Fatalf("LogOutEverywhere = %d, %v; want 0, nil", n, err)
		}
		onlyEntry(t, db, by, "user.log_out_everywhere", id)
	})

	t.Run("unknown user", func(t *testing.T) {
		if _, err := repo.LogOutEverywhere(t.Context(), "00000000-0000-4000-8000-000000000000", testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("LogOutEverywhere = %v, want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("not recorded, not logged out", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		sessions(t, id, time.Now())
		if _, err := repo.LogOutEverywhere(t.Context(), id, brokenActor(t, db)); err == nil {
			t.Fatal("LogOutEverywhere with a broken actor worked")
		}
		if n := countRows(t, db, `SELECT count(*) FROM user_sessions WHERE user_id = $1`, id); n != 1 {
			t.Error("logged out without its record")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositorySetFeature(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)

	features := func(t *testing.T, id string) int {
		t.Helper()
		return countRows(t, db, `SELECT count(*) FROM user_features WHERE user_id = $1`, id)
	}
	has := func(t *testing.T, id string) bool {
		t.Helper()
		u, err := repo.User(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return u.Newsletters
	}

	t.Run("grants once", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		other, _ := actionTestUser(t, db, nil)
		by := testActor(t, db)
		if has(t, id) {
			t.Fatal("a new user has newsletters")
		}

		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, true, by); err != nil {
			t.Fatalf("SetFeature: %v", err)
		}
		var source string
		if err := db.QueryRow(`SELECT source FROM user_features WHERE user_id = $1 AND feature = $2`, id, string(rss.FeatureNewsletters)).Scan(&source); err != nil || source != "admin" {
			t.Errorf("the row's source = %q, %v; want admin", source, err)
		}
		if !has(t, id) {
			t.Error("User doesn't show newsletters on")
		}
		if features(t, other) != 0 {
			t.Error("the other user got the feature")
		}
		e := onlyEntry(t, db, by, "user.feature_grant", id)
		if e.Before != nil || e.After["feature"] != string(rss.FeatureNewsletters) || e.After["source"] != "admin" {
			t.Errorf("audit before %v, after %v", e.Before, e.After)
		}

		// Again: nothing changes, and nothing's recorded.
		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, true, by); err != nil {
			t.Fatalf("SetFeature again: %v", err)
		}
		if n := features(t, id); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
		onlyEntry(t, db, by, "user.feature_grant", id)
	})

	t.Run("revokes once", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, true, testActor(t, db)); err != nil {
			t.Fatal(err)
		}
		by := testActor(t, db)

		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, false, by); err != nil {
			t.Fatalf("SetFeature: %v", err)
		}
		if n := features(t, id); n != 0 {
			t.Errorf("rows = %d, want 0", n)
		}
		if has(t, id) {
			t.Error("User shows newsletters on")
		}
		e := onlyEntry(t, db, by, "user.feature_revoke", id)
		if e.After != nil || e.Before["feature"] != string(rss.FeatureNewsletters) || e.Before["source"] != "admin" {
			t.Errorf("audit before %v, after %v", e.Before, e.After)
		}

		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, false, by); err != nil {
			t.Fatalf("SetFeature again: %v", err)
		}
		onlyEntry(t, db, by, "user.feature_revoke", id)
	})

	t.Run("revoking what they don't have records nothing", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		by := testActor(t, db)
		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, false, by); err != nil {
			t.Fatalf("SetFeature: %v", err)
		}
		if got := auditBy(t, db, by); len(got) != 0 {
			t.Errorf("audit entries = %d, want 0", len(got))
		}
	})

	t.Run("deleting the user deletes their rows", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, true, testActor(t, db)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Delete(t.Context(), id, testActor(t, db)); err != nil {
			t.Fatal(err)
		}
		if n := features(t, id); n != 0 {
			t.Errorf("rows = %d, want 0", n)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		if err := repo.SetFeature(t.Context(), "00000000-0000-4000-8000-000000000000", rss.FeatureNewsletters, true, testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SetFeature = %v, want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("not recorded, not granted", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		if err := repo.SetFeature(t.Context(), id, rss.FeatureNewsletters, true, brokenActor(t, db)); err == nil {
			t.Fatal("SetFeature with a broken actor worked")
		}
		if n := features(t, id); n != 0 {
			t.Error("granted without its record")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositoryDelete(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)
	subs := NewSubscriptionRepository(db)
	reads := NewReadRepository(db)
	tokens := NewTokenRepository(db)

	// rows counts the user's rows in each table the delete cascades to.
	rows := func(t *testing.T, userID string) map[string]int {
		t.Helper()
		got := map[string]int{}
		for _, table := range []string{"users", "subscriptions", "reads", "user_sessions", "user_tokens"} {
			col := "user_id"
			if table == "users" {
				col = "id"
			}
			got[table] = countRows(t, db, `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, userID)
		}
		return got
	}
	// newUser makes a user who follows articleID's feed, has read it, and
	// has a session and a link.
	newUser := func(t *testing.T, articleID string) (string, string) {
		t.Helper()
		id, email := actionTestUser(t, db, nil)
		var feedID string
		if err := db.QueryRow(`SELECT feed_id FROM articles WHERE id = $1`, articleID).Scan(&feedID); err != nil {
			t.Fatal(err)
		}
		if err := subs.Subscribe(t.Context(), id, feedID); err != nil {
			t.Fatal(err)
		}
		if err := reads.MarkRead(t.Context(), id, articleID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO user_sessions (user_id) VALUES ($1)`, id); err != nil {
			t.Fatal(err)
		}
		createToken(t, tokens, id, rss.TokenConfirmEmail, email, time.Now().Add(time.Hour))
		return id, email
	}

	t.Run("cascades as the reader's delete does", func(t *testing.T) {
		articleID := newTestArticles(t, db, 1)[0]
		id, email := newUser(t, articleID)
		other, _ := newUser(t, articleID)
		by := testActor(t, db)

		got, err := repo.Delete(t.Context(), id, by)
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got.ID != id || got.Name != "Action User" || got.Email != email || got.CreatedAt.IsZero() {
			t.Errorf("Delete returned %+v; want the user as they were", got)
		}
		for table, n := range rows(t, id) {
			if n != 0 {
				t.Errorf("%s: %d rows left; want 0", table, n)
			}
		}
		for table, n := range rows(t, other) {
			if n != 1 {
				t.Errorf("the other user's %s: %d rows; want 1", table, n)
			}
		}
		if n := countRows(t, db, `SELECT count(*) FROM articles WHERE id = $1`, articleID); n != 1 {
			t.Error("the article went with the user")
		}
		e := onlyEntry(t, db, by, "user.delete", id)
		if e.Before["email"] != email || e.Before["created_at"] == nil || e.After != nil {
			t.Errorf("audit before %v, after %v", e.Before, e.After)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		if _, err := repo.Delete(t.Context(), "00000000-0000-4000-8000-000000000000", testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Delete = %v, want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("not recorded, not deleted", func(t *testing.T) {
		id, _ := actionTestUser(t, db, nil)
		if _, err := repo.Delete(t.Context(), id, brokenActor(t, db)); err == nil {
			t.Fatal("Delete with a broken actor worked")
		}
		if n := countRows(t, db, `SELECT count(*) FROM users WHERE id = $1`, id); n != 1 {
			t.Error("deleted without its record")
		}
	})
}
