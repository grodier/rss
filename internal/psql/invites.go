package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// InviteRepository stores single-use signup invites (invites rows). Each
// invite's token is stored hashed; UserRepository.CreateFromInvite redeems
// one.
type InviteRepository struct {
	DB *sql.DB
}

func NewInviteRepository(db *sql.DB) *InviteRepository {
	return &InviteRepository{DB: db}
}

// inviteColumns are the columns scanInvite reads, from invites i joined to
// its waitlist entry w.
const inviteColumns = `i.id, coalesce(i.email, ''), coalesce(i.waitlist_id::text, ''), coalesce(w.name, ''),
	i.note, i.wave, i.expires_at, i.redeemed_at, coalesce(i.user_id::text, ''), i.revoked_at, i.created_at`

const inviteFrom = ` FROM invites i LEFT JOIN waitlist w ON w.id = i.waitlist_id`

func scanInvite(row interface{ Scan(...any) error }) (rss.Invite, error) {
	var i rss.Invite
	var redeemedAt, revokedAt sql.NullTime
	err := row.Scan(&i.ID, &i.Email, &i.WaitlistID, &i.Name, &i.Note, &i.Wave,
		&i.ExpiresAt, &redeemedAt, &i.UserID, &revokedAt, &i.CreatedAt)
	i.RedeemedAt = redeemedAt.Time
	i.RevokedAt = revokedAt.Time
	return i, err
}

// Create stores inv (its Email, WaitlistID, Note, Wave and ExpiresAt) with
// the token tokenHash, and returns it as stored.
func (r *InviteRepository) Create(ctx context.Context, inv rss.Invite, tokenHash []byte) (rss.Invite, error) {
	stmt := `WITH i AS (
			INSERT INTO invites (token_hash, email, waitlist_id, note, wave, expires_at)
			VALUES ($1, nullif($2, '')::citext, nullif($3, '')::uuid, $4, $5, $6)
			RETURNING *
		)
		SELECT ` + inviteColumns + ` FROM i LEFT JOIN waitlist w ON w.id = i.waitlist_id`
	return scanInvite(r.DB.QueryRowContext(ctx, stmt, tokenHash, inv.Email, inv.WaitlistID, inv.Note, inv.Wave, inv.ExpiresAt))
}

// GetByToken returns the invite whose token hashes to tokenHash, whatever
// its status, so signup can say why it can't be used; rss.ErrNoRecord if
// there's none.
func (r *InviteRepository) GetByToken(ctx context.Context, tokenHash []byte) (rss.Invite, error) {
	stmt := `SELECT ` + inviteColumns + inviteFrom + ` WHERE i.token_hash = $1`
	inv, err := scanInvite(r.DB.QueryRowContext(ctx, stmt, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Invite{}, rss.ErrNoRecord
	}
	return inv, err
}

// List returns every invite, newest first.
func (r *InviteRepository) List(ctx context.Context) ([]rss.Invite, error) {
	stmt := `SELECT ` + inviteColumns + inviteFrom + ` ORDER BY i.created_at DESC, i.id`
	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []rss.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// Revoke revokes the invite id (a UUID), so its link stops working; an
// invite already revoked keeps its first revoked_at. rss.ErrNoRecord if
// there's no such invite; rss.ErrInviteUnusable if it was already used.
func (r *InviteRepository) Revoke(ctx context.Context, id string) error {
	stmt := `UPDATE invites SET revoked_at = coalesce(revoked_at, now())
		WHERE id = $1 AND redeemed_at IS NULL`
	res, err := r.DB.ExecContext(ctx, stmt, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var exists bool
	if err := r.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM invites WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return rss.ErrInviteUnusable
	}
	return rss.ErrNoRecord
}
