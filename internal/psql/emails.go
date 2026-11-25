package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
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
	_, err := enqueue(ctx, r.DB, m)
	return err
}

// enqueue is Enqueue in q, a transaction or the database, returning the
// new email's ID.
func enqueue(ctx context.Context, q querier, m mail.Message) (string, error) {
	stmt := `WITH e AS (
			INSERT INTO emails (to_address, subject, text_body, html_body)
			SELECT $1, $2, $3, $4
			WHERE (SELECT count(*) FROM emails
				WHERE to_address = $1 AND created_at > now() - interval '1 hour') < $5
			RETURNING id, created_at
		)
		INSERT INTO email_daily_counts (day, queued)
		SELECT (created_at AT TIME ZONE 'UTC')::date, 1 FROM e
		ON CONFLICT (day) DO UPDATE SET queued = email_daily_counts.queued + 1
		RETURNING (SELECT id FROM e)`

	var id string
	err := q.QueryRowContext(ctx, stmt, m.To, m.Subject, m.Text, m.HTML, maxEmailsPerAddressPerHour).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", rss.ErrTooManyEmails
	}
	return id, err
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

// emailDue is true for an email ClaimNext may claim. The admin's Overview
// counts the email queue with it (AdminOverviewRepository.Queues).
const emailDue = `status = 'pending' AND next_attempt_at <= now()`

// ClaimNext takes the pending email due soonest, counts the attempt and
// leases it: no other worker claims it until lease has passed. rss.ErrNoRecord
// when nothing is due.
func (r *EmailRepository) ClaimNext(ctx context.Context, lease time.Duration) (mail.Queued, error) {
	stmt := `UPDATE emails SET next_attempt_at = now() + make_interval(secs => $1), attempts = attempts + 1
		WHERE id = (
			SELECT id FROM emails
			WHERE ` + emailDue + `
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

	return execOne(ctx, r.DB, stmt, id, retryAt, textcut.Bytes(msg, maxErrorLen))
}

// giveUp marks the email $1 failed for good, with the error $2, and clears
// its bodies: GiveUp's statement, and the admin's Cancel's.
const giveUp = `UPDATE emails SET status = 'failed', last_error = $2, text_body = '', html_body = ''
	WHERE id = $1`

// GiveUp marks an email failed for good, with an internal error message
// truncated to maxErrorLen bytes, and clears its bodies.
func (r *EmailRepository) GiveUp(ctx context.Context, id, msg string) error {
	return execOne(ctx, r.DB, giveUp, id, textcut.Bytes(msg, maxErrorLen))
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

// The admin's email page: the outbox's emails without their bodies, and
// its actions on one, each recorded in the audit log in its own
// transaction ("Admin" in docs/decisions.md).

// EmailCanceled is the error a canceled email is given up on with.
const EmailCanceled = "canceled from the admin"

// List returns a page of the outbox's emails (q), newest first, each with
// the account its address belongs to, if any. The page after an email
// that's been deleted since (the mail runner deletes finished emails after
// a week) is empty. The outbox holds about a week of emails, so this reads
// it whole without an index on created_at.
func (r *EmailRepository) List(ctx context.Context, q rss.AdminEmailQuery) ([]rss.AdminEmail, error) {
	stmt := `SELECT e.id, e.to_address, e.subject, e.status, e.attempts, e.next_attempt_at, e.last_error,
			e.created_at, e.sent_at, coalesce(u.id::text, '')
		FROM emails e LEFT JOIN users u ON u.email = e.to_address
		WHERE ($1 = '' OR e.status = $1)
			AND ($2 = '' OR (e.created_at, e.id) < (SELECT created_at, id FROM emails WHERE id = NULLIF($2, '')::uuid))
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $3`

	rows, err := r.DB.QueryContext(ctx, stmt, q.Status, q.After, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rss.AdminEmail
	for rows.Next() {
		var e rss.AdminEmail
		var sent sql.NullTime
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.Status, &e.Attempts, &e.NextAttemptAt, &e.LastError,
			&e.CreatedAt, &sent, &e.UserID); err != nil {
			return nil, err
		}
		e.SentAt = sent.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountByStatus counts the outbox's emails by status, all of them under
// "", in one pass.
func (r *EmailRepository) CountByStatus(ctx context.Context) (map[string]int, error) {
	stmt := `SELECT count(*),
		count(*) FILTER (WHERE status = 'pending'),
		count(*) FILTER (WHERE status = 'sent'),
		count(*) FILTER (WHERE status = 'failed')
		FROM emails`

	var all, pending, sent, failed int
	if err := r.DB.QueryRowContext(ctx, stmt).Scan(&all, &pending, &sent, &failed); err != nil {
		return nil, err
	}
	return map[string]int{"": all, rss.EmailPending: pending, rss.EmailSent: sent, rss.EmailFailed: failed}, nil
}

// lockPendingEmail locks email id's row in tx and returns its address,
// attempts and next attempt. rss.ErrNoRecord if there's no such email;
// rss.ErrEmailNotPending if it was sent or given up on.
func lockPendingEmail(ctx context.Context, tx *sql.Tx, id string) (rss.AdminEmail, error) {
	e := rss.AdminEmail{ID: id}
	err := tx.QueryRowContext(ctx, `SELECT to_address, status, attempts, next_attempt_at FROM emails WHERE id = $1 FOR UPDATE`, id).
		Scan(&e.To, &e.Status, &e.Attempts, &e.NextAttemptAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rss.AdminEmail{}, rss.ErrNoRecord
	case err != nil:
		return rss.AdminEmail{}, err
	case e.Status != rss.EmailPending:
		return rss.AdminEmail{}, rss.ErrEmailNotPending
	}
	return e, nil
}

// Retry makes pending email id due now with its attempts reset, so the
// mail runner sends it within a minute and tries it its full number of
// times again; recorded as email.retry by by. rss.ErrNoRecord if there's no
// such email; rss.ErrEmailNotPending if it was sent or given up on (whose
// bodies are cleared, so it can't be sent again). An email the runner is
// sending at that moment (leased) can be sent twice.
func (r *EmailRepository) Retry(ctx context.Context, id string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	e, err := lockPendingEmail(ctx, tx, id)
	if err != nil {
		return err
	}
	var at time.Time
	if err := tx.QueryRowContext(ctx, `UPDATE emails SET attempts = 0, next_attempt_at = now() WHERE id = $1 RETURNING next_attempt_at`, id).Scan(&at); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "email.retry", TargetType: "email", TargetID: id,
		Before: map[string]any{"attempts": e.Attempts, "next_attempt_at": e.NextAttemptAt},
		After:  map[string]any{"attempts": 0, "next_attempt_at": at, "email_to": e.To}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Cancel gives up on pending email id, as the mail runner does after its
// last attempt (EmailCanceled as its error, its bodies cleared); recorded
// as email.cancel by by. rss.ErrNoRecord if there's no such email;
// rss.ErrEmailNotPending if it was sent or given up on.
func (r *EmailRepository) Cancel(ctx context.Context, id string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	e, err := lockPendingEmail(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, giveUp, id, EmailCanceled); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "email.cancel", TargetType: "email", TargetID: id,
		Before: map[string]any{"status": rss.EmailPending},
		After:  map[string]any{"status": rss.EmailFailed, "last_error": EmailCanceled, "email_to": e.To}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// QueueTest queues m, the test email, as Enqueue does, recorded as
// email.send_test by by, and returns its ID. rss.ErrTooManyEmails over the
// per-address cap, and then nothing is queued.
func (r *EmailRepository) QueueTest(ctx context.Context, m mail.Message, by rss.AuditActor) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	id, err := enqueue(ctx, tx, m)
	if err != nil {
		return "", err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "email.send_test", TargetType: "email", TargetID: id,
		After: map[string]any{"email_to": m.To}})
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}
