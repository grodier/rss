package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
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
// the token tokenHash, and returns it as stored. Its invite.create entry in
// the audit log, by by, is recorded in the same transaction.
func (r *InviteRepository) Create(ctx context.Context, inv rss.Invite, tokenHash []byte, by rss.AuditActor) (rss.Invite, error) {
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
	after := map[string]any{"note": got.Note, "expires_at": got.ExpiresAt}
	if got.Email != "" {
		after["email"] = got.Email
	}
	if got.Wave != "" {
		after["wave"] = got.Wave
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "invite.create", TargetType: "invite", TargetID: got.ID, After: after})
	if err != nil {
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

// Get returns the invite id (a UUID), or rss.ErrNoRecord.
func (r *InviteRepository) Get(ctx context.Context, id string) (rss.Invite, error) {
	stmt := `SELECT ` + inviteColumns + inviteFrom + ` WHERE i.id = $1`
	inv, err := scanInvite(r.DB.QueryRowContext(ctx, stmt, id))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Invite{}, rss.ErrNoRecord
	}
	return inv, err
}

// List returns at most limit invites, newest first: from the newest, or,
// with before (an invite's ID), from the one after it. An unknown before
// returns none.
func (r *InviteRepository) List(ctx context.Context, before string, limit int) ([]rss.Invite, error) {
	stmt := `SELECT ` + inviteColumns + inviteFrom + `
		WHERE $1 = '' OR (i.created_at, i.id) < (SELECT created_at, id FROM invites WHERE id = nullif($1, '')::uuid)
		ORDER BY i.created_at DESC, i.id DESC
		LIMIT $2`
	rows, err := r.DB.QueryContext(ctx, stmt, before, limit)
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

// Waves sums up each wave's invites, newest wave first. An account counts
// as active when one of its sessions was seen 7 or more days after it
// signed up; sessions that ended (logged out, or expired) no longer count.
func (r *InviteRepository) Waves(ctx context.Context) ([]rss.InviteWave, error) {
	stmt := `SELECT i.wave, min(i.created_at), count(*), count(i.redeemed_at),
			count(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM users u JOIN user_sessions s ON s.user_id = u.id
				WHERE u.id = i.user_id AND s.last_seen_at >= u.created_at + interval '7 days'))
		FROM invites i
		WHERE i.wave <> ''
		GROUP BY i.wave
		ORDER BY min(i.created_at) DESC, i.wave`
	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var waves []rss.InviteWave
	for rows.Next() {
		var w rss.InviteWave
		if err := rows.Scan(&w.Label, &w.SentAt, &w.Sent, &w.SignedUp, &w.Active); err != nil {
			return nil, err
		}
		waves = append(waves, w)
	}
	return waves, rows.Err()
}

// Revoke revokes the invite id (a UUID), so its link stops working, and
// records its invite.revoke entry in the audit log, by by, in the same
// transaction. Revoking an invite already revoked changes and records
// nothing. rss.ErrNoRecord if there's no such invite; rss.ErrInviteUnusable
// if it was already used.
func (r *InviteRepository) Revoke(ctx context.Context, id string, by rss.AuditActor) error {
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

	var revoked time.Time
	if err := tx.QueryRowContext(ctx, `UPDATE invites SET revoked_at = now() WHERE id = $1 RETURNING revoked_at`, id).Scan(&revoked); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "invite.revoke", TargetType: "invite", TargetID: id,
		Before: map[string]any{"revoked_at": nil}, After: map[string]any{"revoked_at": revoked}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// sendLock is the advisory lock that serializes sending invites, so two
// admin tabs can't both fit a wave under the same room in the limits.
const sendLock = 574

// SendWave sends w: for each of its invites it stores the invite, bound to
// its waitlist entry with w's label and expiry, gives the entry its new link
// token, and queues the email, all in one transaction with one invite.wave
// entry in the audit log, by by. Invites are sent one batch at a time, and,
// before anything is written, checked against the limits: available is how
// many emails fit with the usage at the time. rss.ErrOverLimit if w's emails
// don't all fit; rss.ErrNoRecord if one of its entries is no longer waiting
// (it left, or was invited since). Either way nothing is sent.
func (r *InviteRepository) SendWave(ctx context.Context, w rss.Wave, available func(rss.EmailUsage) int, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	if _, err := sendBound(ctx, tx, w.Invites, w.Label, "", w.ExpiresAt, available); err != nil {
		return err
	}
	ids := make([]string, len(w.Invites))
	for i, inv := range w.Invites {
		ids[i] = inv.WaitlistID
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "invite.wave",
		After: map[string]any{"wave": w.Label, "count": len(w.Invites), "waitlist_ids": ids, "expires_at": w.ExpiresAt}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SendInvite sends one invite made by hand to a waiting entry, like a wave
// of one: it stores the invite (inv's WaitlistID, Email, Note and
// ExpiresAt), gives the entry its new link token and queues the email, in
// one transaction with its invite.create entry in the audit log, by by. It
// returns the invite's ID. rss.ErrOverLimit and rss.ErrNoRecord as SendWave.
func (r *InviteRepository) SendInvite(ctx context.Context, inv rss.WaveInvite, note string, expiresAt time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	ids, err := sendBound(ctx, tx, []rss.WaveInvite{inv}, "", note, expiresAt, available)
	if err != nil {
		return "", err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "invite.create", TargetType: "invite", TargetID: ids[0],
		After: map[string]any{"email": inv.Email, "note": note, "expires_at": expiresAt}})
	if err != nil {
		return "", err
	}
	return ids[0], tx.Commit()
}

// sendBound stores invites in tx, each bound to its waiting entry, with
// wave, note and expiresAt, gives each entry its new link token and queues
// each email, and returns the invites' IDs. It first takes sendLock, so
// sends happen one at a time, and checks the emails fit (available) and the
// entries are all still waiting: rss.ErrOverLimit or rss.ErrNoRecord when
// not, having written nothing.
func sendBound(ctx context.Context, tx *sql.Tx, invites []rss.WaveInvite, wave, note string, expiresAt time.Time, available func(rss.EmailUsage) int) ([]string, error) {
	if err := takeSendLock(ctx, tx, len(invites), available); err != nil {
		return nil, err
	}

	entries := make([]string, len(invites))
	for i, inv := range invites {
		entries[i] = inv.WaitlistID
	}
	// The row locks make a concurrent confirmation, leave or signup wait.
	var waiting int
	stmt := `SELECT count(*) FROM (
			SELECT 1 FROM waitlist w WHERE w.id = ANY($1::uuid[]) AND ` + waitingEntry + ` FOR UPDATE
		) e`
	if err := tx.QueryRowContext(ctx, stmt, pq.Array(entries)).Scan(&waiting); err != nil {
		return nil, err
	}
	if waiting != len(invites) {
		return nil, rss.ErrNoRecord
	}

	ids := make([]string, len(invites))
	for i, inv := range invites {
		err := tx.QueryRowContext(ctx, `INSERT INTO invites (token_hash, email, waitlist_id, wave, note, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			inv.InviteTokenHash, inv.Email, inv.WaitlistID, wave, note, expiresAt).Scan(&ids[i])
		if err != nil {
			return nil, err
		}
		// The entry is confirmed already, so its old link only left the list:
		// the email's leave link takes its place.
		if err := execOne(ctx, tx, `UPDATE waitlist SET token_hash = $2 WHERE id = $1`, inv.WaitlistID, inv.WaitlistTokenHash); err != nil {
			return nil, err
		}
		m := mail.Message{To: inv.Email, Subject: inv.Subject, Text: inv.Text, HTML: inv.HTML}
		if err := enqueue(ctx, tx, m); err != nil {
			return nil, fmt.Errorf("queue the invite to %s: %w", inv.Email, err)
		}
	}
	return ids, nil
}

// takeSendLock takes sendLock in tx, then checks n more emails fit:
// rss.ErrOverLimit when available, given the usage now, is less than n.
func takeSendLock(ctx context.Context, tx *sql.Tx, n int, available func(rss.EmailUsage) int) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, sendLock); err != nil {
		return err
	}
	usage, err := emailUsage(ctx, tx, time.Now())
	if err != nil {
		return err
	}
	if n > available(usage) {
		return rss.ErrOverLimit
	}
	return nil
}

// Resend gives the bound invite id (a UUID) a new token (inv's
// InviteTokenHash) and expiry, and queues inv's email to the invite's
// address; its waitlist entry, if it still has one, gets inv's
// WaitlistTokenHash, the email's leave link. It's one transaction with its
// invite.resend entry in the audit log, by by, checked against the limits
// like a wave. rss.ErrNoRecord if there's no such invite;
// rss.ErrInviteUnusable if it's open, used or revoked; rss.ErrOverLimit if
// the email doesn't fit.
func (r *InviteRepository) Resend(ctx context.Context, id string, inv rss.WaveInvite, expiresAt time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// The row lock makes a concurrent redemption wait for this to commit.
	var email, waitlistID sql.NullString
	var redeemedAt, revokedAt sql.NullTime
	var oldExpiry time.Time
	err = tx.QueryRowContext(ctx, `SELECT email, waitlist_id, redeemed_at, revoked_at, expires_at FROM invites WHERE id = $1 FOR UPDATE`, id).
		Scan(&email, &waitlistID, &redeemedAt, &revokedAt, &oldExpiry)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rss.ErrNoRecord
	case err != nil:
		return err
	case !email.Valid || redeemedAt.Valid || revokedAt.Valid:
		return rss.ErrInviteUnusable
	}
	if err := takeSendLock(ctx, tx, 1, available); err != nil {
		return err
	}

	if err := execOne(ctx, tx, `UPDATE invites SET token_hash = $2, expires_at = $3 WHERE id = $1`, id, inv.InviteTokenHash, expiresAt); err != nil {
		return err
	}
	if waitlistID.Valid && inv.WaitlistTokenHash != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE waitlist SET token_hash = $2 WHERE id = $1`, waitlistID.String, inv.WaitlistTokenHash); err != nil {
			return err
		}
	}
	m := mail.Message{To: email.String, Subject: inv.Subject, Text: inv.Text, HTML: inv.HTML}
	if err := enqueue(ctx, tx, m); err != nil {
		return fmt.Errorf("queue the invite to %s: %w", email.String, err)
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "invite.resend", TargetType: "invite", TargetID: id,
		Before: map[string]any{"expires_at": oldExpiry}, After: map[string]any{"expires_at": expiresAt}})
	if err != nil {
		return err
	}
	return tx.Commit()
}
