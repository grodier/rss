package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

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
// the token tokenHash, and returns it as stored. audit, the entry recording
// it, is recorded in the same transaction, with its TargetID set to the new
// invite's ID.
func (r *InviteRepository) Create(ctx context.Context, inv rss.Invite, tokenHash []byte, audit rss.AuditEntry) (rss.Invite, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.Invite{}, err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `WITH i AS (
			INSERT INTO invites (token_hash, email, waitlist_id, note, wave, expires_at)
			VALUES ($1, nullif($2, '')::citext, nullif($3, '')::uuid, $4, $5, $6)
			RETURNING *
		)
		SELECT ` + inviteColumns + ` FROM i LEFT JOIN waitlist w ON w.id = i.waitlist_id`
	got, err := scanInvite(tx.QueryRowContext(ctx, stmt, tokenHash, inv.Email, inv.WaitlistID, inv.Note, inv.Wave, inv.ExpiresAt))
	if err != nil {
		return rss.Invite{}, err
	}
	audit.TargetID = got.ID
	if err := recordAudit(ctx, tx, audit); err != nil {
		return rss.Invite{}, err
	}
	return got, tx.Commit()
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

// Revoke revokes the invite id (a UUID), so its link stops working, and
// records audit, the entry recording it, in the same transaction, with its
// TargetID and the revoked_at it changed. Revoking an invite already revoked
// changes and records nothing. rss.ErrNoRecord if there's no such invite;
// rss.ErrInviteUnusable if it was already used.
func (r *InviteRepository) Revoke(ctx context.Context, id string, audit rss.AuditEntry) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// The row lock makes a concurrent redemption wait for this to commit.
	var redeemedAt, revokedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT redeemed_at, revoked_at FROM invites WHERE id = $1 FOR UPDATE`, id).Scan(&redeemedAt, &revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rss.ErrNoRecord
	case err != nil:
		return err
	case redeemedAt.Valid:
		return rss.ErrInviteUnusable
	case revokedAt.Valid:
		return nil
	}

	var now time.Time
	if err := tx.QueryRowContext(ctx, `UPDATE invites SET revoked_at = now() WHERE id = $1 RETURNING revoked_at`, id).Scan(&now); err != nil {
		return err
	}
	audit.TargetID = id
	audit.Before = map[string]any{"revoked_at": nil}
	audit.After = map[string]any{"revoked_at": now}
	if err := recordAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}
