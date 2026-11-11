package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// AdminOverviewRepository reads the admin's Overview: counts across every
// reader, never one reader's rows.
type AdminOverviewRepository struct {
	DB *sql.DB
}

func NewAdminOverviewRepository(db *sql.DB) *AdminOverviewRepository {
	return &AdminOverviewRepository{DB: db}
}

// estimatedRows is the planner's estimate of a table's rows, from its last
// ANALYZE: counting the big tables (articles, reads, timeline_entries)
// exactly would read them whole. A table never analyzed reports -1, so it
// counts as 0 until autovacuum gets to it. See "The admin's overview counts
// big tables" in docs/backlog.md.
func estimatedRows(table string) string {
	return `(SELECT greatest(reltuples, 0)::bigint FROM pg_class WHERE oid = '` + table + `'::regclass)`
}

// Overview counts the People and Content sections at now, in one round
// trip. The small tables are counted exactly; the big ones estimated.
func (r *AdminOverviewRepository) Overview(ctx context.Context, now time.Time) (rss.AdminOverview, error) {
	stmt := `SELECT
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM users WHERE email_confirmed_at IS NOT NULL),
			(SELECT count(*) FROM users WHERE created_at > $1::timestamptz - interval '7 days'),
			(SELECT count(*) FROM users WHERE created_at > $1::timestamptz - interval '30 days'),
			(SELECT count(DISTINCT user_id) FROM user_sessions WHERE last_seen_at > $1::timestamptz - interval '7 days'),
			(SELECT count(DISTINCT user_id) FROM user_sessions WHERE last_seen_at > $1::timestamptz - interval '30 days'),
			(SELECT count(*) FROM waitlist w WHERE ` + waitingEntry + `),
			(SELECT count(*) FROM invites WHERE redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > $1),
			(SELECT count(*) FROM sites),
			(SELECT count(*) FROM feeds),
			(SELECT count(*) FROM feeds f WHERE EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id)),
			(SELECT count(*) FROM subscriptions),
			` + estimatedRows("articles") + `,
			` + estimatedRows("reads") + `,
			` + estimatedRows("timeline_entries")

	var o rss.AdminOverview
	p, c := &o.People, &o.Content
	err := r.DB.QueryRowContext(ctx, stmt, now).Scan(
		&p.Accounts, &p.ConfirmedAccounts, &p.Signups7, &p.Signups30, &p.Active7, &p.Active30,
		&p.Waiting, &p.InvitesPending,
		&c.Sites, &c.Feeds, &c.SubscribedFeeds, &c.Subscriptions,
		&c.Articles, &c.Reads, &c.TimelineEntries)
	return o, err
}
