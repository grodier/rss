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

// Queues counts each background queue's due jobs and finds when the oldest
// was due, in the order the Overview lists them, in one round trip. Each
// uses its claim query's own condition (feedDue, siteIconDue, …), so it
// counts what the runner would take. Lookups and import feeds have no due
// time: they're due from when they were asked for.
func (r *AdminOverviewRepository) Queues(ctx context.Context) ([]rss.QueueStatus, error) {
	stmt := `SELECT name, jobs, oldest FROM (
		SELECT 1, 'lookup', count(*), min(requested_at) FROM lookups WHERE ` + lookupPending + `
		UNION ALL
		SELECT 2, 'import', count(*), min(pi.created_at)
			FROM import_feeds p JOIN imports pi ON pi.id = p.import_id WHERE ` + importFeedPending + `
		UNION ALL
		SELECT 3, 'refresh', count(*), min(next_fetch_at) FROM feeds WHERE ` + feedDue + `
		UNION ALL
		SELECT 4, 'icon', count(*), min(icon_next_check_at) FROM sites WHERE ` + siteIconDue + `
		UNION ALL
		SELECT 5, 'enrich', count(*), min(j.next_attempt_at)
			FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE ` + enrichJobDue + `
		UNION ALL
		SELECT 6, 'mail', count(*), min(next_attempt_at) FROM emails WHERE ` + emailDue + `
		) q (ord, name, jobs, oldest) ORDER BY ord`

	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queues []rss.QueueStatus
	for rows.Next() {
		var q rss.QueueStatus
		var oldest sql.NullTime
		if err := rows.Scan(&q.Name, &q.Jobs, &oldest); err != nil {
			return nil, err
		}
		q.Oldest = oldest.Time
		queues = append(queues, q)
	}
	return queues, rows.Err()
}

// Jobs sums the jobs each runner finished since since, by outcome, from
// job_metrics' minute rows: they're kept 7 days (metrics.Housekeeper), so
// a window of a day needs no hourly rows and is exact to the minute.
// Runners and outcomes are in name order. recorded is whether job_metrics
// has any rows at all, so the Overview can tell "no jobs lately" from "no
// metrics yet".
func (r *AdminOverviewRepository) Jobs(ctx context.Context, since time.Time) (outcomes []rss.JobOutcomes, recorded bool, err error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT runner, outcome, sum(jobs)::int
		FROM job_metrics
		WHERE resolution = 'minute' AND period_start >= $1
		GROUP BY runner, outcome
		ORDER BY runner, outcome`, since)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var runner string
		var o rss.JobOutcome
		if err := rows.Scan(&runner, &o.Name, &o.Jobs); err != nil {
			return nil, false, err
		}
		if n := len(outcomes); n == 0 || outcomes[n-1].Runner != runner {
			outcomes = append(outcomes, rss.JobOutcomes{Runner: runner})
		}
		last := &outcomes[len(outcomes)-1]
		last.Outcomes = append(last.Outcomes, o)
		last.Total += o.Jobs
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(outcomes) > 0 {
		return outcomes, true, nil
	}
	// Nothing in the window: one more look, for whether anything was ever
	// recorded.
	err = r.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM job_metrics)`).Scan(&recorded)
	return nil, recorded, err
}
