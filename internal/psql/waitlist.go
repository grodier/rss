package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// WaitlistRepository stores the people waiting for an invite (waitlist
// rows). Each entry has one link token, stored hashed, which both confirms
// it and leaves the list.
type WaitlistRepository struct {
	DB *sql.DB
}

func NewWaitlistRepository(db *sql.DB) *WaitlistRepository {
	return &WaitlistRepository{DB: db}
}

const waitlistColumns = `id, email, name, sites, source, confirmed_at, created_at`

func scanWaitlistEntry(row interface{ Scan(...any) error }) (rss.WaitlistEntry, error) {
	var e rss.WaitlistEntry
	var confirmedAt sql.NullTime
	err := row.Scan(&e.ID, &e.Email, &e.Name, &e.Sites, &e.Source, &confirmedAt, &e.CreatedAt)
	e.ConfirmedAt = confirmedAt.Time
	return e, err
}

// Join adds e to the waitlist, unconfirmed, with the link token tokenHash.
// An unconfirmed entry with the same address gets e's name, sites and
// source and the new hash, so only the newest link works; it keeps its
// created_at. rss.ErrDuplicateEmail if the address is already confirmed.
func (r *WaitlistRepository) Join(ctx context.Context, e rss.WaitlistEntry, tokenHash []byte) (rss.WaitlistEntry, error) {
	stmt := `INSERT INTO waitlist (email, name, sites, source, token_hash)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			sites = EXCLUDED.sites,
			source = EXCLUDED.source,
			token_hash = EXCLUDED.token_hash
		WHERE waitlist.confirmed_at IS NULL
		RETURNING ` + waitlistColumns

	row := r.DB.QueryRowContext(ctx, stmt, e.Email, e.Name, e.Sites, e.Source, tokenHash)
	got, err := scanWaitlistEntry(row)
	if err != nil {
		// The conflict's WHERE skipped the update: the address is confirmed.
		if errors.Is(err, sql.ErrNoRows) {
			return rss.WaitlistEntry{}, rss.ErrDuplicateEmail
		}
		return rss.WaitlistEntry{}, err
	}
	return got, nil
}

// GetByToken returns the entry whose link token hashes to tokenHash, or
// rss.ErrNoRecord.
func (r *WaitlistRepository) GetByToken(ctx context.Context, tokenHash []byte) (rss.WaitlistEntry, error) {
	stmt := `SELECT ` + waitlistColumns + ` FROM waitlist WHERE token_hash = $1`
	e, err := scanWaitlistEntry(r.DB.QueryRowContext(ctx, stmt, tokenHash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.WaitlistEntry{}, rss.ErrNoRecord
		}
		return rss.WaitlistEntry{}, err
	}
	return e, nil
}

// GetByEmail returns the entry with email, confirmed or not, or
// rss.ErrNoRecord.
func (r *WaitlistRepository) GetByEmail(ctx context.Context, email string) (rss.WaitlistEntry, error) {
	stmt := `SELECT ` + waitlistColumns + ` FROM waitlist WHERE email = $1`
	e, err := scanWaitlistEntry(r.DB.QueryRowContext(ctx, stmt, email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.WaitlistEntry{}, rss.ErrNoRecord
		}
		return rss.WaitlistEntry{}, err
	}
	return e, nil
}

// Confirm confirms the entry whose link token hashes to tokenHash. An entry
// already confirmed keeps its first confirmed_at. rss.ErrNoRecord if there's
// no such entry.
func (r *WaitlistRepository) Confirm(ctx context.Context, tokenHash []byte) error {
	stmt := `UPDATE waitlist SET confirmed_at = coalesce(confirmed_at, now()) WHERE token_hash = $1`
	return execWaitlistToken(ctx, r.DB, stmt, tokenHash)
}

// Leave deletes the entry whose link token hashes to tokenHash.
// rss.ErrNoRecord if there's no such entry.
func (r *WaitlistRepository) Leave(ctx context.Context, tokenHash []byte) error {
	return execWaitlistToken(ctx, r.DB, `DELETE FROM waitlist WHERE token_hash = $1`, tokenHash)
}

// execWaitlistToken runs stmt, which affects the entry with link token
// tokenHash; rss.ErrNoRecord if it affected none.
func execWaitlistToken(ctx context.Context, db *sql.DB, stmt string, tokenHash []byte) error {
	res, err := db.ExecContext(ctx, stmt, tokenHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return rss.ErrNoRecord
	}
	return nil
}

// pendingInvite is true for a waitlist row w with an invite that can still
// be used: the admin's Invited tab.
const pendingInvite = `EXISTS (SELECT 1 FROM invites i WHERE i.waitlist_id = w.id
	AND i.redeemed_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now())`

// waitingEntry is true for a waitlist row w that's confirmed and has no
// pending invite: the admin's Waiting tab, the only entries a wave invites.
// An entry whose invite expired or was revoked is waiting again.
const waitingEntry = `w.confirmed_at IS NOT NULL AND NOT ` + pendingInvite

// waitlistTabs are the admin's tabs' conditions on a waitlist row w.
var waitlistTabs = map[string]string{
	rss.WaitlistWaiting:     waitingEntry,
	rss.WaitlistInvited:     `w.confirmed_at IS NOT NULL AND ` + pendingInvite,
	rss.WaitlistUnconfirmed: `w.confirmed_at IS NULL`,
}

// Counts returns how many entries each of the admin's tabs has.
func (r *WaitlistRepository) Counts(ctx context.Context) (rss.WaitlistCounts, error) {
	stmt := `SELECT
			count(*) FILTER (WHERE confirmed AND NOT pending),
			count(*) FILTER (WHERE confirmed AND pending),
			count(*) FILTER (WHERE NOT confirmed)
		FROM (SELECT w.confirmed_at IS NOT NULL AS confirmed, ` + pendingInvite + ` AS pending FROM waitlist w) e`

	var c rss.WaitlistCounts
	err := r.DB.QueryRowContext(ctx, stmt).Scan(&c.Waiting, &c.Invited, &c.Unconfirmed)
	return c, err
}

// List returns at most limit of the entries on tab (rss.WaitlistWaiting,
// WaitlistInvited or WaitlistUnconfirmed), oldest first, after skipping
// offset, each with its latest invite.
func (r *WaitlistRepository) List(ctx context.Context, tab string, limit, offset int) ([]rss.WaitlistRow, error) {
	where, ok := waitlistTabs[tab]
	if !ok {
		return nil, fmt.Errorf("unknown waitlist tab %q", tab)
	}
	stmt := `SELECT w.id, w.email, w.name, w.sites, w.source, w.confirmed_at, w.created_at,
			coalesce(i.id::text, ''), coalesce(i.wave, ''), i.expires_at, i.redeemed_at, i.revoked_at, i.created_at
		FROM waitlist w
		LEFT JOIN LATERAL (
			SELECT * FROM invites i WHERE i.waitlist_id = w.id ORDER BY i.created_at DESC, i.id LIMIT 1
		) i ON true
		WHERE ` + where + `
		ORDER BY w.created_at, w.id
		LIMIT $1 OFFSET $2`

	rows, err := r.DB.QueryContext(ctx, stmt, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []rss.WaitlistRow
	for rows.Next() {
		var row rss.WaitlistRow
		var confirmedAt, expiresAt, redeemedAt, revokedAt, invitedAt sql.NullTime
		err := rows.Scan(&row.ID, &row.Email, &row.Name, &row.Sites, &row.Source, &confirmedAt, &row.CreatedAt,
			&row.Invite.ID, &row.Invite.Wave, &expiresAt, &redeemedAt, &revokedAt, &invitedAt)
		if err != nil {
			return nil, err
		}
		row.ConfirmedAt = confirmedAt.Time
		row.Invite.ExpiresAt, row.Invite.RedeemedAt, row.Invite.RevokedAt, row.Invite.CreatedAt =
			expiresAt.Time, redeemedAt.Time, revokedAt.Time, invitedAt.Time
		list = append(list, row)
	}
	return list, rows.Err()
}

// ListWaiting returns the waiting entries (confirmed, with no pending
// invite) among ids, which must be UUIDs, oldest first.
func (r *WaitlistRepository) ListWaiting(ctx context.Context, ids []string) ([]rss.WaitlistEntry, error) {
	stmt := `SELECT ` + waitlistColumns + ` FROM waitlist w
		WHERE w.id = ANY($1::uuid[]) AND ` + waitingEntry + `
		ORDER BY w.created_at, w.id`
	return r.listEntries(ctx, stmt, pq.Array(ids))
}

// Oldest returns the n oldest waiting entries, oldest first.
func (r *WaitlistRepository) Oldest(ctx context.Context, n int) ([]rss.WaitlistEntry, error) {
	stmt := `SELECT ` + waitlistColumns + ` FROM waitlist w
		WHERE ` + waitingEntry + `
		ORDER BY w.created_at, w.id
		LIMIT $1`
	return r.listEntries(ctx, stmt, n)
}

func (r *WaitlistRepository) listEntries(ctx context.Context, stmt string, args ...any) ([]rss.WaitlistEntry, error) {
	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []rss.WaitlistEntry
	for rows.Next() {
		e, err := scanWaitlistEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
