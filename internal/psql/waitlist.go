package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
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

const waitlistColumns = `id, email, name, current_reader, reading_interests, source, confirmed_at, created_at`

func scanWaitlistEntry(row interface{ Scan(...any) error }) (rss.WaitlistEntry, error) {
	var e rss.WaitlistEntry
	var confirmedAt sql.NullTime
	err := row.Scan(&e.ID, &e.Email, &e.Name, &e.CurrentReader, &e.ReadingInterests, &e.Source, &confirmedAt, &e.CreatedAt)
	e.ConfirmedAt = confirmedAt.Time
	return e, err
}

// Join adds e to the waitlist, unconfirmed, with the link token tokenHash.
// An unconfirmed entry with the same address gets e's name, answers and
// source and the new hash, so only the newest link works; it keeps its
// created_at. rss.ErrDuplicateEmail if the address is already confirmed.
func (r *WaitlistRepository) Join(ctx context.Context, e rss.WaitlistEntry, tokenHash []byte) (rss.WaitlistEntry, error) {
	stmt := `INSERT INTO waitlist (email, name, current_reader, reading_interests, source, token_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			current_reader = EXCLUDED.current_reader,
			reading_interests = EXCLUDED.reading_interests,
			source = EXCLUDED.source,
			token_hash = EXCLUDED.token_hash
		WHERE waitlist.confirmed_at IS NULL
		RETURNING ` + waitlistColumns

	row := r.DB.QueryRowContext(ctx, stmt, e.Email, e.Name, e.CurrentReader, e.ReadingInterests, e.Source, tokenHash)
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
