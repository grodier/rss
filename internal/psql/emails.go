package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
)

// maxEmailsPerAddressPerHour caps the emails queued to one address in any
// hour, whatever the flow: the backstop against using the app to flood an
// inbox. See "Accounts" in docs/decisions.md.
const maxEmailsPerAddressPerHour = 20

// EmailRepository is the outbox: emails queued by the server and sent by
// mail.Runner.
type EmailRepository struct {
	DB *sql.DB
}

func NewEmailRepository(db *sql.DB) *EmailRepository {
	return &EmailRepository{DB: db}
}

// Enqueue adds m to the outbox, due now, and counts it in today's
// email_daily_counts row. rss.ErrTooManyEmails when its address (ignoring
// case) already has maxEmailsPerAddressPerHour emails queued in the last
// hour.
func (r *EmailRepository) Enqueue(ctx context.Context, m mail.Message) error {
	return enqueue(ctx, r.DB, m)
}

// enqueue is Enqueue in q, a transaction or the database.
func enqueue(ctx context.Context, q querier, m mail.Message) error {
	stmt := `WITH e AS (
			INSERT INTO emails (to_address, subject, text_body, html_body)
			SELECT $1, $2, $3, $4
			WHERE (SELECT count(*) FROM emails
				WHERE to_address = $1 AND created_at > now() - interval '1 hour') < $5
			RETURNING created_at
		)
		INSERT INTO email_daily_counts (day, queued)
		SELECT (created_at AT TIME ZONE 'UTC')::date, 1 FROM e
		ON CONFLICT (day) DO UPDATE SET queued = email_daily_counts.queued + 1`

	err := execOne(ctx, q, stmt, m.To, m.Subject, m.Text, m.HTML, maxEmailsPerAddressPerHour)
	if errors.Is(err, rss.ErrNoRecord) {
		return rss.ErrTooManyEmails
	}
	return err
}

// Usage is how many emails count against the email provider's limits at
// now: those queued in the last 24 hours, plus any older ones still pending,
// which go out in the next; and those queued in now's UTC calendar month.
func (r *EmailRepository) Usage(ctx context.Context, now time.Time) (rss.EmailUsage, error) {
	return emailUsage(ctx, r.DB, now)
}

func emailUsage(ctx context.Context, q querier, now time.Time) (rss.EmailUsage, error) {
	stmt := `SELECT
		(SELECT count(*) FROM emails WHERE created_at > $1::timestamptz - interval '24 hours' OR status = 'pending'),
		(SELECT coalesce(sum(queued), 0) FROM email_daily_counts
			WHERE day >= date_trunc('month', $1::timestamptz AT TIME ZONE 'UTC')::date
			AND day <= ($1::timestamptz AT TIME ZONE 'UTC')::date)`

	var u rss.EmailUsage
	err := q.QueryRowContext(ctx, stmt, now).Scan(&u.Last24h, &u.ThisMonth)
	return u, err
}

// ClaimNext takes the pending email due soonest, counts the attempt and
// leases it: no other worker claims it until lease has passed. rss.ErrNoRecord
// when nothing is due.
func (r *EmailRepository) ClaimNext(ctx context.Context, lease time.Duration) (mail.Queued, error) {
	stmt := `UPDATE emails SET next_attempt_at = now() + make_interval(secs => $1), attempts = attempts + 1
		WHERE id = (
			SELECT id FROM emails
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, to_address, subject, text_body, html_body, attempts`

	var q mail.Queued
	err := r.DB.QueryRowContext(ctx, stmt, lease.Seconds()).Scan(
		&q.ID, &q.Message.To, &q.Message.Subject, &q.Message.Text, &q.Message.HTML, &q.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return mail.Queued{}, rss.ErrNoRecord
	}
	return q, err
}

// MarkSent records that an email was sent and clears its bodies, which may
// carry one-time links.
func (r *EmailRepository) MarkSent(ctx context.Context, id string) error {
	stmt := `UPDATE emails SET status = 'sent', sent_at = now(), text_body = '', html_body = ''
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id)
}

// MarkFailed records a failed attempt, with an internal error message
// truncated to maxErrorLen bytes, and leaves the email pending until
// retryAt.
func (r *EmailRepository) MarkFailed(ctx context.Context, id, msg string, retryAt time.Time) error {
	stmt := `UPDATE emails SET next_attempt_at = $2, last_error = $3
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, retryAt, truncateUTF8(msg, maxErrorLen))
}

// GiveUp marks an email failed for good, with an internal error message
// truncated to maxErrorLen bytes, and clears its bodies.
func (r *EmailRepository) GiveUp(ctx context.Context, id, msg string) error {
	stmt := `UPDATE emails SET status = 'failed', last_error = $2, text_body = '', html_body = ''
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, truncateUTF8(msg, maxErrorLen))
}

// DeleteFinished deletes sent and failed emails created before before, and
// returns how many it deleted. Pending emails are never deleted.
func (r *EmailRepository) DeleteFinished(ctx context.Context, before time.Time) (int64, error) {
	stmt := `DELETE FROM emails WHERE status IN ('sent', 'failed') AND created_at < $1`

	res, err := r.DB.ExecContext(ctx, stmt, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
