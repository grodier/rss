package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// adminTestUser inserts a user named name with email, created at
// createdAt and confirmed at confirmedAt (nil for not), deleted on cleanup,
// and returns its ID.
func adminTestUser(t *testing.T, db *sql.DB, name, email string, createdAt time.Time, confirmedAt any) string {
	t.Helper()
	var id string
	err := db.QueryRow(`INSERT INTO users (name, email, hashed_password, created_at, email_confirmed_at)
		VALUES ($1, $2, '', $3, $4) RETURNING id`, name, email, createdAt, confirmedAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositoryListUsers(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)
	// A prefix unique to the test, so a search for it finds only its users.
	p := fmt.Sprintf("au%d", psqltest.Unique())
	base := time.Now().Add(-time.Hour).Truncate(time.Second)

	oldest := adminTestUser(t, db, p+" Zed", "zed-"+p+"@example.com", base, nil)
	// Same second: ordered by ID among themselves.
	sameA := adminTestUser(t, db, "Same", strings.ToUpper(p)+"-a@example.com", base.Add(time.Minute), nil)
	sameB := adminTestUser(t, db, "Same", p+"-b@example.com", base.Add(time.Minute), base)
	newest := adminTestUser(t, db, strings.ToUpper(p)+" Newest", "newest-"+p+"@example.com", base.Add(2*time.Minute), base)
	// A literal underscore, which a LIKE pattern would take as any
	// character.
	underscore := adminTestUser(t, db, "Under", p+"_x@example.com", base.Add(3*time.Minute), nil)

	now := time.Now().Truncate(time.Microsecond)
	for _, seen := range []time.Time{now.Add(-48 * time.Hour), now.Add(-time.Hour)} {
		if _, err := db.Exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, $2)`, newest, seen); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(rows []rss.AdminUserRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	idDesc := func(a, b string) []string {
		if a > b {
			return []string{a, b}
		}
		return []string{b, a}
	}
	same := idDesc(sameA, sameB)

	tests := []struct {
		name string
		q    string
		want []string
	}{
		// The start of the email or the name, any case.
		{"prefix", p, append(append([]string{underscore, newest}, same...), oldest)},
		{"upper case", strings.ToUpper(p), append(append([]string{underscore, newest}, same...), oldest)},
		{"name only", p + " z", []string{oldest}},
		{"email only", "newest-" + p, []string{newest}},
		{"underscore is literal", p + "_", []string{underscore}},
		{"not the middle", p[2:], nil},
		{"none", p + "nobody", nil},
	}
	for _, tt := range tests {
		got, err := repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: tt.q, Limit: 10})
		if err != nil {
			t.Fatalf("%s: ListUsers: %v", tt.name, err)
		}
		if !slices.Equal(ids(got), tt.want) {
			t.Errorf("%s: ListUsers(%q) = %v, want %v", tt.name, tt.q, ids(got), tt.want)
		}
	}

	// Paging: the next page starts after the last row, through the users
	// who joined in the same second.
	all := append(append([]string{underscore, newest}, same...), oldest)
	var paged []string
	var after rss.AdminUserCursor
	for range len(all) {
		got, err := repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: p, After: after, Limit: 2})
		if err != nil {
			t.Fatalf("ListUsers after %+v: %v", after, err)
		}
		if len(got) == 0 {
			break
		}
		paged = append(paged, ids(got)...)
		last := got[len(got)-1]
		after = rss.AdminUserCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if !slices.Equal(paged, all) {
		t.Errorf("paging by 2 = %v, want %v", paged, all)
	}

	// Without a search, every user, newest first.
	rows, err := repo.ListUsers(t.Context(), rss.AdminUserQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(rows, func(a, b rss.AdminUserRow) int { return b.CreatedAt.Compare(a.CreatedAt) }) {
		t.Error("ListUsers without a search isn't newest first")
	}

	got, err := repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: "newest-" + p, Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("ListUsers newest = %v, %v", got, err)
	}
	u := got[0]
	if u.Name != strings.ToUpper(p)+" Newest" || u.Email != "newest-"+p+"@example.com" ||
		!u.CreatedAt.Equal(base.Add(2*time.Minute)) || !u.EmailConfirmedAt.Equal(base) {
		t.Errorf("row = %+v", u)
	}
	if !u.LastSeenAt.Equal(now.Add(-time.Hour)) {
		t.Errorf("LastSeenAt = %v, want the newest session's, %v", u.LastSeenAt, now.Add(-time.Hour))
	}
	got, err = repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: "zed-" + p, Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("ListUsers zed = %v, %v", got, err)
	}
	if !got[0].LastSeenAt.IsZero() || !got[0].EmailConfirmedAt.IsZero() {
		t.Errorf("a user never seen or confirmed = %+v, want zero times", got[0])
	}

	// A page still follows a cursor whose user was deleted since.
	newestRow, err := repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: "newest-" + p, Limit: 1})
	if err != nil || len(newestRow) != 1 {
		t.Fatalf("ListUsers newest = %v, %v", newestRow, err)
	}
	cursor := rss.AdminUserCursor{CreatedAt: newestRow[0].CreatedAt, ID: newestRow[0].ID}
	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, newest); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.ListUsers(t.Context(), rss.AdminUserQuery{Q: p, After: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := append(append([]string{}, same...), oldest); !slices.Equal(ids(rows), want) {
		t.Errorf("after a deleted user = %v, want %v", ids(rows), want)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminUserRepositoryUser(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminUserRepository(db)
	p := fmt.Sprintf("au%d", psqltest.Unique())
	email := p + "@example.com"
	created := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	id := adminTestUser(t, db, "Reader", email, created, created.Add(time.Minute))
	other := newTestUser(t, db)

	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	// Devices: two seen recently, one past the idle timeout (not counted,
	// but still its last seen if it were the newest), and another user's.
	exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, now()), ($1, now() - interval '1 day'), ($1, now() - interval '100 days'), ($2, now())`, id, other)

	// Subscriptions, saves and stars on articles of two feeds.
	articles := newTestArticles(t, db, 3)
	feedA := newTestFeed(t, db)
	var feedB string
	if err := db.QueryRow(`SELECT feed_id FROM articles WHERE id = $1`, articles[0]).Scan(&feedB); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO subscriptions (user_id, feed_id) VALUES ($1, $2), ($1, $3), ($4, $2)`, id, feedA.ID, feedB, other)
	exec(`INSERT INTO saves (user_id, article_id) VALUES ($1, $2), ($1, $3), ($1, $4), ($5, $2)`, id, articles[0], articles[1], articles[2], other)
	exec(`INSERT INTO stars (user_id, article_id) VALUES ($1, $2)`, id, articles[0])

	// Tags and timelines, one shared.
	exec(`INSERT INTO tags (user_id, name) VALUES ($1, 'a'), ($1, 'b'), ($1, 'c'), ($2, 'a')`, id, other)
	exec(`INSERT INTO timelines (user_id, name, share_token) VALUES ($1, 'One', NULL), ($1, 'Two', $2)`, id, p)

	// Two imports: their feeds subscribed (or already), failed and pending.
	var imp1, imp2 string
	if err := db.QueryRow(`INSERT INTO imports (user_id) VALUES ($1) RETURNING id`, id).Scan(&imp1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO imports (user_id) VALUES ($1) RETURNING id`, id).Scan(&imp2); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO import_feeds (import_id, position, url, status, failure) VALUES
		($1, 0, 'https://a.example/', 'subscribed', ''),
		($1, 1, 'https://b.example/', 'failed', 'unreachable'),
		($2, 0, 'https://c.example/', 'already_subscribed', ''),
		($2, 1, 'https://d.example/', 'pending', ''),
		($2, 2, 'https://e.example/', 'failed', 'not_feed')`, imp1, imp2)

	// Emails to the address, in any case, and one to another address.
	exec(`INSERT INTO emails (to_address, subject, text_body, html_body, status, attempts, created_at, sent_at) VALUES
		($1, 'Confirm your email', 'secret link', '<p>secret link</p>', 'sent', 1, now() - interval '2 hours', now() - interval '2 hours'),
		($2, 'Reset your password', 'secret link', '', 'pending', 0, now() - interval '1 hour', NULL),
		($3, 'Not theirs', '', '', 'sent', 1, now(), now()),
		($4, 'Confirm your new address', '', '', 'sent', 1, now() - interval '30 minutes', now() - interval '30 minutes'),
		($1, 'A previous account''s', '', '', 'sent', 1, $5::timestamptz - interval '1 hour', $5::timestamptz - interval '1 hour'),
		($1, 'Confirm on signup', '', '', 'sent', 1, $5::timestamptz - interval '400 milliseconds', $5)`,
		email, strings.ToUpper(email), "other-"+email, "new-"+email, created)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM emails WHERE to_address IN ($1, $2, $3)`, email, "other-"+email, "new-"+email)
	})

	// Email links: one outstanding, one expired.
	exec(`INSERT INTO user_tokens (hash, user_id, purpose, email, expires_at) VALUES
		($1, $2, 'change_email', $3, now() + interval '1 day'),
		($4, $2, 'confirm_email', $5, now() - interval '1 minute')`,
		[]byte(p+"-1"), id, "new-"+email, []byte(p+"-2"), email)

	// The invite they signed up with.
	exec(`INSERT INTO invites (token_hash, wave, expires_at, redeemed_at, user_id) VALUES ($1, 'Wave 3', now() + interval '1 day', now(), $2)`, []byte(p), id)
	t.Cleanup(func() { db.Exec(`DELETE FROM invites WHERE token_hash = $1`, []byte(p)) })

	got, err := repo.User(t.Context(), id)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if got.ID != id || got.Name != "Reader" || got.Email != email || !got.CreatedAt.Equal(created) || !got.EmailConfirmedAt.Equal(created.Add(time.Minute)) {
		t.Errorf("account = %+v", got.AdminUserRow)
	}
	if got.LastSeenAt.IsZero() || time.Since(got.LastSeenAt) > time.Minute {
		t.Errorf("LastSeenAt = %v, want the newest session's, about now", got.LastSeenAt)
	}
	if got.Invite.ID == "" || got.Invite.Wave != "Wave 3" || got.Invite.UserID != id {
		t.Errorf("Invite = %+v, want Wave 3's", got.Invite)
	}
	want := rss.AdminUserCounts{Devices: 2, Subscriptions: 2, Tags: 3, Timelines: 2, SharedTimelines: 1,
		Saved: 3, Starred: 1, Imports: 2, ImportedFeeds: 2, ImportFailures: 2}
	if got.Counts != want {
		t.Errorf("Counts = %+v, want %+v", got.Counts, want)
	}
	var subjects []string
	for _, e := range got.Emails {
		subjects = append(subjects, e.Subject)
	}
	// Those to the address and the change of address's, since they joined
	// (within the second users.created_at rounds to), newest first.
	if want := []string{"Confirm your new address", "Reset your password", "Confirm your email", "Confirm on signup"}; !slices.Equal(subjects, want) {
		t.Errorf("Emails' subjects = %v, want %v", subjects, want)
	}
	if len(got.Emails) == 4 {
		got.Emails = got.Emails[1:3]
		if e := got.Emails[1]; e.Status != "sent" || e.Attempts != 1 || e.SentAt.IsZero() || e.To != email {
			t.Errorf("sent email = %+v", e)
		}
		if e := got.Emails[0]; e.Status != "pending" || e.Attempts != 0 || !e.SentAt.IsZero() {
			t.Errorf("pending email = %+v", e)
		}
	}
	if len(got.Tokens) != 1 || got.Tokens[0].Purpose != rss.TokenChangeEmail || got.Tokens[0].Email != "new-"+email {
		t.Errorf("Tokens = %+v, want the change_email link only", got.Tokens)
	}

	// Another user with nothing: zero counts, no invite, emails or links.
	bare, err := repo.User(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	if bare.Invite.ID != "" || bare.Emails != nil || bare.Tokens != nil {
		t.Errorf("bare user = %+v, want no invite, emails or links", bare)
	}
	if want := (rss.AdminUserCounts{Devices: 1, Subscriptions: 1, Tags: 1, Saved: 1}); bare.Counts != want {
		t.Errorf("bare user's Counts = %+v, want %+v", bare.Counts, want)
	}

	if _, err := repo.User(t.Context(), "00000000-0000-4000-8000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("User(unknown) = %v, want ErrNoRecord", err)
	}
}
