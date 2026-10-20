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

// Enqueue adds m to the outbox, due now. rss.ErrTooManyEmails when its
// address (ignoring case) already has maxEmailsPerAddressPerHour emails
// queued in the last hour.
func (r *EmailRepository) Enqueue(ctx context.Context, m mail.Message) error {
	stmt := `INSERT INTO emails (to_address, subject, text_body, html_body)
		SELECT $1, $2, $3, $4
		WHERE (SELECT count(*) FROM emails
			WHERE to_address = $1 AND created_at > now() - interval '1 hour') < $5`

	err := execOne(ctx, r.DB, stmt, m.To, m.Subject, m.Text, m.HTML, maxEmailsPerAddressPerHour)
	if errors.Is(err, rss.ErrNoRecord) {
		return rss.ErrTooManyEmails
	}
	return err
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
