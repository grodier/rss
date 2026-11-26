package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
)

// The admin's actions on a reader's account, each recorded in the audit log
// in its own transaction ("Admin" in docs/decisions.md). Each locks the
// user's row first, so two actions on one account run one after the other.

// lockUser locks user id's row in tx and returns its address and when it
// was confirmed (zero if not). rss.ErrNoRecord if there's no such user.
func lockUser(ctx context.Context, tx *sql.Tx, id string) (rss.User, error) {
	u := rss.User{ID: id}
	var confirmed sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT name, email, email_confirmed_at, created_at FROM users WHERE id = $1 FOR UPDATE`, id).
		Scan(&u.Name, &u.Email, &confirmed, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.User{}, rss.ErrNoRecord
	}
	u.EmailConfirmedAt = confirmed.Time
	return u, err
}

// linkActions are the audit log's actions for the links an admin sends, by
// the token's purpose.
var linkActions = map[string]string{
	rss.TokenConfirmEmail:  "user.resend_confirmation",
	rss.TokenResetPassword: "user.send_password_reset",
}

// SendLink stores t, a confirmation or password reset link's token, and
// queues m, its email, recorded as user.resend_confirmation or
// user.send_password_reset by by. Like a new link from the reader, it first
// deletes the user's expired links. rss.ErrNoRecord if the user is gone;
// rss.ErrAddressChanged if their address (in any case) is no longer t's;
// rss.ErrAlreadyConfirmed for a confirmation to a confirmed address;
// rss.ErrTooManyEmails over the per-address cap. Then nothing is stored or
// queued.
func (r *AdminUserRepository) SendLink(ctx context.Context, t rss.Token, m mail.Message, by rss.AuditActor) error {
	action, ok := linkActions[t.Purpose]
	if !ok {
		return fmt.Errorf("psql: SendLink of a %s link", t.Purpose)
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	u, err := lockUser(ctx, tx, t.UserID)
	switch {
	case err != nil:
		return err
	case !strings.EqualFold(u.Email, t.Email):
		return rss.ErrAddressChanged
	case t.Purpose == rss.TokenConfirmEmail && !u.EmailConfirmedAt.IsZero():
		return rss.ErrAlreadyConfirmed
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_tokens WHERE user_id = $1 AND expires_at <= now()`, t.UserID); err != nil {
		return err
	}
	stmt := `INSERT INTO user_tokens (hash, user_id, purpose, email, expires_at) VALUES ($1, $2, $3, $4, $5)`
	if _, err := tx.ExecContext(ctx, stmt, t.Hash, t.UserID, t.Purpose, t.Email, t.ExpiresAt); err != nil {
		return err
	}
	if _, err := enqueue(ctx, tx, m); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: action, TargetType: "user", TargetID: t.UserID,
		After: map[string]any{"email_to": m.To, "link_expires_at": t.ExpiresAt}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MarkConfirmed confirms user id's address, email, now, for one confirmed
// another way, and, as confirming with a link does, deletes their
// confirmation links; recorded as user.mark_confirmed by by. rss.ErrNoRecord
// if there's no such user; rss.ErrAddressChanged if their address (in any
// case) is no longer email; rss.ErrAlreadyConfirmed if it's already
// confirmed.
func (r *AdminUserRepository) MarkConfirmed(ctx context.Context, id, email string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	u, err := lockUser(ctx, tx, id)
	switch {
	case err != nil:
		return err
	case !strings.EqualFold(u.Email, email):
		return rss.ErrAddressChanged
	case !u.EmailConfirmedAt.IsZero():
		return rss.ErrAlreadyConfirmed
	}
	var at time.Time
	if err := tx.QueryRowContext(ctx, `UPDATE users SET email_confirmed_at = now() WHERE id = $1 RETURNING email_confirmed_at`, id).Scan(&at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_tokens WHERE user_id = $1 AND purpose = 'confirm_email'`, id); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "user.mark_confirmed", TargetType: "user", TargetID: id,
		Before: map[string]any{"email_confirmed_at": nil}, After: map[string]any{"email": u.Email, "email_confirmed_at": at}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// LogOutEverywhere deletes all of user id's sessions, recorded as
// user.log_out_everywhere by by. It returns how many devices that logged
// out: the sessions seen within rss.SessionIdleTimeout, as the user's page
// counts them (an older one no longer logs anyone in). rss.ErrNoRecord if
// there's no such user.
func (r *AdminUserRepository) LogOutEverywhere(ctx context.Context, id string, by rss.AuditActor) (int, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := lockUser(ctx, tx, id); err != nil {
		return 0, err
	}
	stmt := `WITH d AS (DELETE FROM user_sessions WHERE user_id = $1 RETURNING last_seen_at)
		SELECT count(*) FILTER (WHERE last_seen_at > now() - make_interval(secs => $2)) FROM d`
	var devices int
	if err := tx.QueryRowContext(ctx, stmt, id, rss.SessionIdleTimeout.Seconds()).Scan(&devices); err != nil {
		return 0, err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "user.log_out_everywhere", TargetType: "user", TargetID: id,
		Before: map[string]any{"devices": devices}, After: map[string]any{"devices": 0}})
	if err != nil {
		return 0, err
	}
	return devices, tx.Commit()
}

// SetFeature grants user id feature when on, as granted by an admin, or
// revokes it, recorded as user.feature_grant or user.feature_revoke by by
// when that changed anything: granting a feature they have, or revoking
// one they don't, records nothing. rss.ErrNoRecord if there's no such user.
func (r *AdminUserRepository) SetFeature(ctx context.Context, id string, feature rss.Feature, on bool, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := lockUser(ctx, tx, id); err != nil {
		return err
	}
	// Each returns the row's source when it changed a row, and no row when
	// it didn't.
	stmt, action := `DELETE FROM user_features WHERE user_id = $1 AND feature = $2 RETURNING source`, "user.feature_revoke"
	if on {
		stmt = `INSERT INTO user_features (user_id, feature, source) VALUES ($1, $2, 'admin') ON CONFLICT DO NOTHING RETURNING source`
		action = "user.feature_grant"
	}
	var source string
	err = tx.QueryRowContext(ctx, stmt, id, string(feature)).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// The row as it was, or as it now is: a grant's entry has only After, a
	// revoke's only Before.
	row := map[string]any{"feature": string(feature), "source": source}
	e := rss.AuditEntry{AuditActor: by, Action: action, TargetType: "user", TargetID: id}
	if on {
		e.After = row
	} else {
		e.Before = row
	}
	if err := recordAudit(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete deletes user id's account as the reader's own delete does
// (deleteUser), without their password, recorded as user.delete by by. It
// returns the user as they were, for the email saying so. rss.ErrNoRecord if
// there's no such user.
func (r *AdminUserRepository) Delete(ctx context.Context, id string, by rss.AuditActor) (rss.User, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.User{}, err
	}
	defer tx.Rollback()

	u, err := lockUser(ctx, tx, id)
	if err != nil {
		return rss.User{}, err
	}
	if err := deleteUser(ctx, tx, id); err != nil {
		return rss.User{}, err
	}
	// The address, so the log says whose account it was once it's gone.
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "user.delete", TargetType: "user", TargetID: id,
		Before: map[string]any{"email": u.Email, "created_at": u.CreatedAt}})
	if err != nil {
		return rss.User{}, err
	}
	return u, tx.Commit()
}
