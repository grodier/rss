package psql

import (
	"database/sql"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
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

	// A new, unconfirmed account seen today, and a confirmed one from 20
	// days ago last seen 10 days ago.
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

	// A site with a subscribed feed and an unsubscribed one.
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
