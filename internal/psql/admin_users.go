package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/grodier/rss/internal/rss"
)

// AdminUserRepository reads the admin's users pages: account facts and
// counts, never a reader's reads, saves, stars, tags, timelines or feeds
// ("Admin" in docs/decisions.md).
type AdminUserRepository struct {
	DB *sql.DB
}

func NewAdminUserRepository(db *sql.DB) *AdminUserRepository {
	return &AdminUserRepository{DB: db}
}

// adminUserEmails is how many of the emails sent to a reader's address
// their page lists, newest first. The outbox keeps finished emails for a
// week (mail.Runner.KeepFor), so it's rarely reached.
const adminUserEmails = 50

// adminUserColumns are the columns scanAdminUserRow reads, from users u:
// the last seen time is the newest of their sessions'.
const adminUserColumns = `u.id, u.name, u.email, u.email_confirmed_at, u.created_at,
	(SELECT max(us.last_seen_at) FROM user_sessions us WHERE us.user_id = u.id)`

func scanAdminUserRow(row interface{ Scan(...any) error }) (rss.AdminUserRow, error) {
	var u rss.AdminUserRow
	var confirmed, lastSeen sql.NullTime
	err := row.Scan(&u.ID, &u.Name, &u.Email, &confirmed, &u.CreatedAt, &lastSeen)
	u.EmailConfirmedAt, u.LastSeenAt = confirmed.Time, lastSeen.Time
	return u, err
}

// ListUsers returns a page of readers, newest first, whose email or name
// starts with q.Q (any case), starting after the user q.After. Users who
// joined in the same second are ordered by ID, so a page's last row is the
// next page's cursor. It reads every user (see "The admin's users list" in
// docs/backlog.md).
func (r *AdminUserRepository) ListUsers(ctx context.Context, q rss.AdminUserQuery) ([]rss.AdminUserRow, error) {
	args := []any{q.Limit}
	where := []string{"true"}
	if q.Q != "" {
		args = append(args, prefixPattern(q.Q))
		where = append(where, fmt.Sprintf(`(u.email ILIKE $%[1]d ESCAPE '\' OR u.name ILIKE $%[1]d ESCAPE '\')`, len(args)))
	}
	if q.After != "" {
		args = append(args, q.After)
		where = append(where, fmt.Sprintf(`(u.created_at, u.id) < (SELECT c.created_at, c.id FROM users c WHERE c.id = $%d)`, len(args)))
	}
	stmt := `SELECT ` + adminUserColumns + `
		FROM users u
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT $1`

	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminUserRow
	for rows.Next() {
		u, err := scanAdminUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// User returns reader id's page, or rss.ErrNoRecord: their account, the
// invite they signed up with, their counts, the newest adminUserEmails
// emails sent to their address and their email links that haven't expired.
func (r *AdminUserRepository) User(ctx context.Context, id string) (rss.AdminUser, error) {
	var out rss.AdminUser
	var err error
	out.AdminUserRow, err = scanAdminUserRow(r.DB.QueryRowContext(ctx, `SELECT `+adminUserColumns+` FROM users u WHERE u.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.AdminUser{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.AdminUser{}, err
	}

	stmt := `SELECT ` + inviteColumns + inviteFrom + ` WHERE i.user_id = $1 ORDER BY i.redeemed_at DESC LIMIT 1`
	out.Invite, err = scanInvite(r.DB.QueryRowContext(ctx, stmt, id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return rss.AdminUser{}, err
	}

	if out.Counts, err = r.counts(ctx, id); err != nil {
		return rss.AdminUser{}, err
	}
	if out.Emails, err = r.emails(ctx, out.Email); err != nil {
		return rss.AdminUser{}, err
	}
	if out.Tokens, err = r.tokens(ctx, id); err != nil {
		return rss.AdminUser{}, err
	}
	return out, nil
}

// counts counts user id's devices, subscriptions, tags, timelines (and
// those shared), saved and starred articles, and imports with their feeds'
// outcomes, in one statement. Devices are the sessions seen within the
// reader's idle timeout (90 days, server.sessionIdleTimeout): an older one
// is only deleted at the reader's next login.
func (r *AdminUserRepository) counts(ctx context.Context, id string) (rss.AdminUserCounts, error) {
	stmt := `SELECT
		(SELECT count(*) FROM user_sessions WHERE user_id = $1 AND last_seen_at > now() - interval '90 days'),
		(SELECT count(*) FROM subscriptions WHERE user_id = $1),
		(SELECT count(*) FROM tags WHERE user_id = $1),
		(SELECT count(*) FROM timelines WHERE user_id = $1),
		(SELECT count(*) FROM timelines WHERE user_id = $1 AND share_token IS NOT NULL),
		(SELECT count(*) FROM saves WHERE user_id = $1),
		(SELECT count(*) FROM stars WHERE user_id = $1),
		(SELECT count(*) FROM imports WHERE user_id = $1),
		(SELECT count(*) FILTER (WHERE f.status IN ('subscribed', 'already_subscribed')) FROM import_feeds f JOIN imports i ON i.id = f.import_id WHERE i.user_id = $1),
		(SELECT count(*) FILTER (WHERE f.status = 'failed') FROM import_feeds f JOIN imports i ON i.id = f.import_id WHERE i.user_id = $1)`

	var c rss.AdminUserCounts
	err := r.DB.QueryRowContext(ctx, stmt, id).Scan(&c.Devices, &c.Subscriptions, &c.Tags, &c.Timelines, &c.SharedTimelines,
		&c.Saved, &c.Starred, &c.Imports, &c.ImportedFeeds, &c.ImportFailures)
	return c, err
}

// emails returns the newest adminUserEmails emails in the outbox sent to
// address, without their bodies.
func (r *AdminUserRepository) emails(ctx context.Context, address string) ([]rss.AdminUserEmail, error) {
	stmt := `SELECT id, subject, status, attempts, created_at, sent_at
		FROM emails
		WHERE to_address = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`
	rows, err := r.DB.QueryContext(ctx, stmt, address, adminUserEmails)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminUserEmail
	for rows.Next() {
		var e rss.AdminUserEmail
		var sent sql.NullTime
		if err := rows.Scan(&e.ID, &e.Subject, &e.Status, &e.Attempts, &e.CreatedAt, &sent); err != nil {
			return nil, err
		}
		e.SentAt = sent.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// tokens returns user id's email links that haven't expired, newest first,
// without their hashes.
func (r *AdminUserRepository) tokens(ctx context.Context, id string) ([]rss.AdminUserToken, error) {
	stmt := `SELECT purpose, email, created_at, expires_at
		FROM user_tokens
		WHERE user_id = $1 AND expires_at > now()
		ORDER BY created_at DESC`
	rows, err := r.DB.QueryContext(ctx, stmt, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminUserToken
	for rows.Next() {
		var t rss.AdminUserToken
		if err := rows.Scan(&t.Purpose, &t.Email, &t.CreatedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
