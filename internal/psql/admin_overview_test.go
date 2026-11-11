package psql

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB. Other tests share the
// database, so it checks how much each count grows by the rows it creates,
// not totals.
func TestAdminOverviewRepositoryOverview(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminOverviewRepository(db)
	now := dbNow(t, db)

	before, err := repo.Overview(t.Context(), now)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	// A new, unconfirmed account seen today on two devices (counted once),
	// and a confirmed one from 20 days ago last seen 10 days ago.
	newReader := newTestUser(t, db)
	oldReader := newTestUser(t, db)
	exec(`UPDATE users SET email_confirmed_at = $2, created_at = $2 WHERE id = $1`, oldReader, now.Add(-20*24*time.Hour))
	exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, $2), ($1, $2)`, newReader, now)
	exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, $2)`, oldReader, now.Add(-10*24*time.Hour))
	// An account from 40 days ago, seen 40 days ago: in neither window.
	ancient := newTestUser(t, db)
	exec(`UPDATE users SET created_at = $2 WHERE id = $1`, ancient, now.Add(-40*24*time.Hour))
	exec(`INSERT INTO user_sessions (user_id, last_seen_at) VALUES ($1, $2)`, ancient, now.Add(-40*24*time.Hour))

	// Waitlist: one confirmed and waiting, one confirmed with a pending
	// invite, one unconfirmed; one open invite pending, one expired, one
	// used and one revoked.
	newOverviewWaitlistEntry(t, db, true)
	invited := newOverviewWaitlistEntry(t, db, true)
	newOverviewWaitlistEntry(t, db, false)
	newOverviewInvite(t, db, invited, now.Add(time.Hour), "")
	newOverviewInvite(t, db, "", now.Add(time.Hour), "")
	newOverviewInvite(t, db, "", now.Add(-time.Hour), "")
	newOverviewInvite(t, db, "", now.Add(time.Hour), "redeemed_at")
	newOverviewInvite(t, db, "", now.Add(time.Hour), "revoked_at")

	// Two sites, one with a subscribed feed and one with an unsubscribed
	// feed.
	newSubscribedFeed(t, db, newReader)
	newTestFeed(t, db)

	after, err := repo.Overview(t.Context(), now)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	for _, c := range []struct {
		name          string
		before, after int
		want          int
	}{
		{"Accounts", before.People.Accounts, after.People.Accounts, 3},
		{"ConfirmedAccounts", before.People.ConfirmedAccounts, after.People.ConfirmedAccounts, 1},
		{"Signups7", before.People.Signups7, after.People.Signups7, 1},
		{"Signups30", before.People.Signups30, after.People.Signups30, 2},
		{"Active7", before.People.Active7, after.People.Active7, 1},
		{"Active30", before.People.Active30, after.People.Active30, 2},
		{"Waiting", before.People.Waiting, after.People.Waiting, 1},
		{"InvitesPending", before.People.InvitesPending, after.People.InvitesPending, 2},
		{"Sites", before.Content.Sites, after.Content.Sites, 2},
		{"Feeds", before.Content.Feeds, after.Content.Feeds, 2},
		{"SubscribedFeeds", before.Content.SubscribedFeeds, after.Content.SubscribedFeeds, 1},
		{"Subscriptions", before.Content.Subscriptions, after.Content.Subscriptions, 1},
	} {
		if got := c.after - c.before; got != c.want {
			t.Errorf("%s grew by %d, want %d", c.name, got, c.want)
		}
	}

	// The estimates are the planner's, so only their sign is certain.
	for name, n := range map[string]int{
		"Articles": after.Content.Articles, "Reads": after.Content.Reads, "TimelineEntries": after.Content.TimelineEntries,
	} {
		if n < 0 {
			t.Errorf("%s = %d, want >= 0", name, n)
		}
	}
}

// newOverviewWaitlistEntry adds a waitlist entry, confirmed or not, deleted
// on cleanup, and returns its ID.
func newOverviewWaitlistEntry(t *testing.T, db *sql.DB, confirmed bool) string {
	t.Helper()
	email := newWaitlistEmail(t, db)
	var id string
	err := db.QueryRow(`INSERT INTO waitlist (email, name, token_hash, confirmed_at)
		VALUES ($1, 'Test', $2, CASE WHEN $3 THEN now() END) RETURNING id`,
		email, newWaitlistHash(t), confirmed).Scan(&id)
	if err != nil {
		t.Fatalf("insert waitlist entry: %v", err)
	}
	return id
}

// newOverviewInvite adds an invite expiring at expires, sent to the waitlist
// entry waitlistID ("" for an open invite), with ended ("redeemed_at",
// "revoked_at" or "" for neither) set to now; it's deleted on cleanup.
func newOverviewInvite(t *testing.T, db *sql.DB, waitlistID string, expires time.Time, ended string) {
	t.Helper()
	var id string
	err := db.QueryRow(`INSERT INTO invites (token_hash, waitlist_id, expires_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3) RETURNING id`,
		newWaitlistHash(t), waitlistID, expires).Scan(&id)
	if err != nil {
		t.Fatalf("insert invite: %v", err)
	}
	cleanupExec(t, db, `DELETE FROM invites WHERE id = $1`, id)
	if ended != "" {
		if _, err := db.Exec(`UPDATE invites SET `+ended+` = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("set %s: %v", ended, err)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB. Other tests share the
// database, so it checks how much each queue grows by the rows it creates,
// and makes its due rows older than any other test's so they're the oldest.
func TestAdminOverviewRepositoryQueues(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminOverviewRepository(db)
	now := dbNow(t, db)
	longAgo := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)

	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	before, err := repo.Queues(t.Context())
	if err != nil {
		t.Fatalf("Queues: %v", err)
	}

	user := newTestUser(t, db)

	// Lookups: one pending, one running.
	key := fmt.Sprintf("queues-test-%d.example.com", time.Now().UnixNano())
	exec(`INSERT INTO lookups (site_key, url, requested_at) VALUES ($1, 'https://x/', $2), ($1 || '/running', 'https://x/', $2)`, key, longAgo)
	exec(`UPDATE lookups SET status = 'running' WHERE site_key = $1 || '/running'`, key)
	cleanupExec(t, db, `DELETE FROM lookups WHERE site_key IN ($1, $1 || '/running')`, key)

	// Imports: one feed pending, one done.
	importID := newTestImport(t, NewImportRepository(db), user, "https://a.example/feed", "https://b.example/feed")
	exec(`UPDATE imports SET created_at = $2 WHERE id = $1`, importID, longAgo)
	exec(`UPDATE import_feeds SET status = 'subscribed' WHERE import_id = $1 AND position = 1`, importID)

	// Feeds: subscribed, gone (subscribed), unsubscribed, and requested;
	// their articles' enrichment jobs first, since saving a fetch sets
	// next_fetch_at.
	subscribed := newSubscribedFeed(t, db, user)
	gone := newSubscribedFeed(t, db, user)
	unsubscribed := newTestFeed(t, db)
	requested := newTestFeed(t, db)
	newEnrichJob(t, db, subscribed, "queues-due", 0, longAgo)
	newEnrichJob(t, db, subscribed, "queues-later", 0, later)
	newEnrichJob(t, db, unsubscribed, "queues-unsubscribed", 0, longAgo)

	for _, f := range []rss.Feed{subscribed, gone, unsubscribed} {
		setNextFetch(t, db, f.ID, longAgo)
	}
	exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, gone.ID)
	setNextFetch(t, db, requested.ID, longAgo.Add(time.Minute))
	setRefreshRequested(t, db, requested.ID)

	// Sites' icons: checked and subscribed (due), checked and unsubscribed
	// (not due), never checked (due), and not yet due.
	setIcon := func(siteID string, checked bool, next time.Time) {
		t.Helper()
		exec(`UPDATE sites SET icon_checked_at = CASE WHEN $2 THEN now() END, icon_next_check_at = $3 WHERE id = $1`, siteID, checked, next)
	}
	setIcon(subscribed.SiteID, true, longAgo)
	setIcon(unsubscribed.SiteID, true, longAgo)
	setIcon(requested.SiteID, false, longAgo.Add(time.Minute))
	setIcon(gone.SiteID, false, later)

	// Emails: pending and due, pending later, and sent.
	addr := fmt.Sprintf("queues-test-%d@example.com", time.Now().UnixNano())
	exec(`INSERT INTO emails (to_address, subject, text_body, html_body, status, next_attempt_at)
		VALUES ($1, 's', '', '', 'pending', $2), ($1, 's', '', '', 'pending', $3), ($1, 's', '', '', 'sent', $2)`, addr, longAgo, later)
	cleanupExec(t, db, `DELETE FROM emails WHERE to_address = $1`, addr)

	after, err := repo.Queues(t.Context())
	if err != nil {
		t.Fatalf("Queues: %v", err)
	}

	want := []struct {
		name string
		grew int
	}{
		{"lookup", 1}, {"import", 1}, {"refresh", 2}, {"icon", 2}, {"enrich", 1}, {"mail", 1},
	}
	if len(before) != len(want) || len(after) != len(want) {
		t.Fatalf("Queues returned %d then %d queues, want %d", len(before), len(after), len(want))
	}
	for i, w := range want {
		if after[i].Name != w.name {
			t.Errorf("queue %d = %q, want %q", i, after[i].Name, w.name)
			continue
		}
		if got := after[i].Jobs - before[i].Jobs; got != w.grew {
			t.Errorf("%s grew by %d, want %d", w.name, got, w.grew)
		}
		if !after[i].Oldest.Equal(longAgo) {
			t.Errorf("%s's oldest = %v, want %v", w.name, after[i].Oldest, longAgo)
		}
	}
}
