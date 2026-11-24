package psql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/rss"
)

// AdminQueuesRepository reads the admin's queues page and retries or
// deletes failed jobs.
type AdminQueuesRepository struct {
	DB *sql.DB
}

func NewAdminQueuesRepository(db *sql.DB) *AdminQueuesRepository {
	return &AdminQueuesRepository{DB: db}
}

// The failed jobs of each queue the page acts on: a failed lookup or
// import feed, and an enrichment job that has failed and waits to be tried
// again (one given up on is deleted). A job's first attempt, still under
// its lease, has no error yet, so it isn't one.
const (
	lookupFailed     = `status = 'failed'`
	importFeedFailed = `status = 'failed'`
	enrichJobFailed  = `attempts > 0 AND last_error <> ''`
)

// Counts counts each queue's jobs by state, in the page's order (lookups,
// imports, enrichment, icons, email), in one round trip. Pending uses each
// claim query's own condition, as the Overview's Queues does.
func (r *AdminQueuesRepository) Counts(ctx context.Context) ([]rss.AdminQueueCounts, error) {
	stmt := `SELECT name, pending, running, failed, overdue, oldest FROM (
		SELECT 1, 'lookup',
			count(*) FILTER (WHERE ` + lookupPending + `),
			count(*) FILTER (WHERE status = 'running'),
			count(*) FILTER (WHERE ` + lookupFailed + `),
			0::bigint,
			min(requested_at) FILTER (WHERE ` + lookupPending + `)
			FROM lookups
		UNION ALL
		SELECT 2, 'import',
			count(*) FILTER (WHERE ` + importFeedPending + `),
			count(*) FILTER (WHERE p.status = 'running'),
			count(*) FILTER (WHERE p.` + importFeedFailed + `),
			0,
			min(pi.created_at) FILTER (WHERE ` + importFeedPending + `)
			FROM import_feeds p JOIN imports pi ON pi.id = p.import_id
		UNION ALL
		SELECT 3, 'enrich',
			count(*) FILTER (WHERE ` + enrichJobDue + `),
			0,
			count(*) FILTER (WHERE j.` + enrichJobFailed + `),
			0,
			min(j.next_attempt_at) FILTER (WHERE ` + enrichJobDue + `)
			FROM enrich_jobs j JOIN articles a ON a.id = j.article_id
		UNION ALL
		SELECT 4, 'icon', count(*), 0, 0,
			count(*) FILTER (WHERE icon_next_check_at < now() - make_interval(secs => $1)),
			min(icon_next_check_at)
			FROM sites WHERE ` + siteIconDue + `
		UNION ALL
		SELECT 5, 'mail',
			count(*) FILTER (WHERE ` + emailDue + `),
			0,
			count(*) FILTER (WHERE status = 'failed'),
			0,
			min(next_attempt_at) FILTER (WHERE ` + emailDue + `)
			FROM emails
		) q (ord, name, pending, running, failed, overdue, oldest) ORDER BY ord`

	rows, err := r.DB.QueryContext(ctx, stmt, rss.QueueBehindAfter.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queues []rss.AdminQueueCounts
	for rows.Next() {
		var q rss.AdminQueueCounts
		var oldest sql.NullTime
		if err := rows.Scan(&q.Name, &q.Pending, &q.Running, &q.Failed, &q.Overdue, &oldest); err != nil {
			return nil, err
		}
		q.Oldest = oldest.Time
		queues = append(queues, q)
	}
	return queues, rows.Err()
}

// JobsPerHour sums each runner's jobs, of every outcome, in each of the
// hours before now, back to since, from job_metrics' minute rows (kept 7
// days). Each hour ends at now less a whole number of hours, so the last
// isn't cut short. Hours with no jobs are left out; runners and hours are
// in order.
func (r *AdminQueuesRepository) JobsPerHour(ctx context.Context, since, now time.Time) ([]rss.JobsHour, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT runner, ago, sum(jobs)::int FROM (
			SELECT runner, floor(extract(epoch FROM $2::timestamptz - period_start) / 3600)::int AS ago, jobs
			FROM job_metrics
			WHERE resolution = 'minute' AND period_start >= $1 AND period_start < $2
		) m
		GROUP BY runner, ago
		ORDER BY runner, ago DESC`, since, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hours []rss.JobsHour
	for rows.Next() {
		var h rss.JobsHour
		var ago int
		if err := rows.Scan(&h.Runner, &ago, &h.Jobs); err != nil {
			return nil, err
		}
		h.End = now.Add(-time.Duration(ago) * time.Hour)
		hours = append(hours, h)
	}
	return hours, rows.Err()
}

// Failures returns every failed job of queue (rss.QueueLookup, QueueImport
// or QueueEnrich), newest first, for the page to group by error. See "The
// admin's queues page reads every failed job" in docs/backlog.md.
func (r *AdminQueuesRepository) Failures(ctx context.Context, queue string) ([]rss.QueueFailure, error) {
	var stmt string
	switch queue {
	case rss.QueueLookup:
		stmt = `SELECT id::text, error, '', finished_at, url, COALESCE(site_id::text, ''), '', NULL::timestamptz
			FROM lookups WHERE ` + lookupFailed + `
			ORDER BY finished_at DESC NULLS LAST, id`
	case rss.QueueImport:
		stmt = `SELECT p.import_id::text || '.' || p.position, p.error, p.failure, p.finished_at, p.url, '',
				COALESCE(p.feed_id::text, ''), pi.created_at
			FROM import_feeds p JOIN imports pi ON pi.id = p.import_id
			WHERE p.` + importFeedFailed + `
			ORDER BY p.finished_at DESC NULLS LAST, p.import_id, p.position`
	case rss.QueueEnrich:
		stmt = `SELECT j.article_id::text, j.last_error, '', j.created_at, a.url, '', a.feed_id::text, NULL::timestamptz
			FROM enrich_jobs j JOIN articles a ON a.id = j.article_id
			WHERE j.` + enrichJobFailed + `
			ORDER BY j.created_at DESC, j.article_id`
	default:
		return nil, fmt.Errorf("failures: unknown queue %q", queue)
	}

	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var failures []rss.QueueFailure
	for rows.Next() {
		var f rss.QueueFailure
		var at, importedAt sql.NullTime
		if err := rows.Scan(&f.ID, &f.Error, &f.Failure, &at, &f.URL, &f.SiteID, &f.FeedID, &importedAt); err != nil {
			return nil, err
		}
		f.At, f.ImportedAt = at.Time, importedAt.Time
		failures = append(failures, f)
	}
	return failures, rows.Err()
}

// queueTargets are the audit log's target types of each queue's jobs.
var queueTargets = map[string]string{
	rss.QueueLookup: "lookup",
	rss.QueueImport: "import_feed",
	rss.QueueEnrich: "enrich_job",
}

// Retry makes those of queue's jobs ids that are still failed due now:
// a lookup or import feed pending again with its attempts and error
// cleared, an enrichment job due now. group is the error of the group the
// ids were listed under, "" for one job. It's recorded as queue.retry by
// by, with the count, on the job when there's one, else with the group's
// error; when none was still failed, there's nothing to record.
func (r *AdminQueuesRepository) Retry(ctx context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error) {
	var stmt string
	switch queue {
	case rss.QueueLookup:
		stmt = `UPDATE lookups SET status = 'pending', attempts = 0, error = '', requested_at = now(),
				started_at = NULL, finished_at = NULL
			WHERE id = ANY($1::uuid[]) AND ` + lookupFailed
	case rss.QueueImport:
		stmt = `UPDATE import_feeds p SET status = 'pending', attempts = 0, failure = '', error = '',
				started_at = NULL, finished_at = NULL
			FROM unnest($1::uuid[], $2::int[]) AS i(import_id, position)
			WHERE p.import_id = i.import_id AND p.position = i.position AND p.` + importFeedFailed
	case rss.QueueEnrich:
		stmt = `UPDATE enrich_jobs SET next_attempt_at = now()
			WHERE article_id = ANY($1::uuid[]) AND ` + enrichJobFailed
	default:
		return 0, fmt.Errorf("retry: unknown queue %q", queue)
	}
	return r.act(ctx, queue, "queue.retry", stmt, ids, group, by)
}

// Delete deletes those of queue's jobs ids that are still failed: a
// lookup, which is also its site key's cache, so the next lookup of the
// site runs afresh, or an enrichment job, whose article keeps what it has.
// Import feeds can't be deleted: they're their import's results. It's
// recorded as queue.delete, as Retry records queue.retry.
func (r *AdminQueuesRepository) Delete(ctx context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error) {
	var stmt string
	switch queue {
	case rss.QueueLookup:
		stmt = `DELETE FROM lookups WHERE id = ANY($1::uuid[]) AND ` + lookupFailed
	case rss.QueueEnrich:
		stmt = `DELETE FROM enrich_jobs WHERE article_id = ANY($1::uuid[]) AND ` + enrichJobFailed
	default:
		return 0, fmt.Errorf("delete: can't delete from queue %q", queue)
	}
	return r.act(ctx, queue, "queue.delete", stmt, ids, group, by)
}

// act runs stmt on queue's jobs ids and records action with how many it
// changed, in one transaction. An import feed's ID is split into its
// import's ID and its position, passed as $1 and $2; the others' are $1.
func (r *AdminQueuesRepository) act(ctx context.Context, queue, action, stmt string, ids []string, group string, by rss.AuditActor) (int, error) {
	args := []any{pq.Array(ids)}
	if queue == rss.QueueImport {
		imports, positions, err := splitImportFeedIDs(ids)
		if err != nil {
			return 0, err
		}
		args = []any{pq.Array(imports), pq.Array(positions)}
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, err
	}
	n64, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	n := int(n64)
	if n == 0 {
		return 0, nil
	}
	entry := rss.AuditEntry{AuditActor: by, Action: action, TargetType: queueTargets[queue], After: map[string]any{"count": n}}
	if group == "" && len(ids) == 1 {
		entry.TargetID = ids[0]
	} else {
		entry.After["group"] = group
	}
	if err := recordAudit(ctx, tx, entry); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// splitImportFeedIDs splits import feeds' IDs, "{import_id}.{position}",
// into their imports' IDs and their positions.
func splitImportFeedIDs(ids []string) (imports []string, positions []int64, err error) {
	for _, id := range ids {
		importID, pos, ok := strings.Cut(id, ".")
		p, perr := strconv.ParseInt(pos, 10, 32)
		if !ok || perr != nil {
			return nil, nil, fmt.Errorf("import feed ID %q isn't {import_id}.{position}", id)
		}
		imports, positions = append(imports, importID), append(positions, p)
	}
	return imports, positions, nil
}
