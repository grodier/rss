package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/grodier/rss/internal/password"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// AdminRepository stores admin accounts (admins rows) and their recovery
// codes, apart from readers' users.
type AdminRepository struct {
	DB *sql.DB
}

func NewAdminRepository(db *sql.DB) *AdminRepository {
	return &AdminRepository{DB: db}
}

const adminColumns = `id, email, totp_secret, totp_last_step, created_at`

func scanAdmin(row interface{ Scan(...any) error }) (rss.Admin, error) {
	var a rss.Admin
	err := row.Scan(&a.ID, &a.Email, &a.TOTPSecret, &a.TOTPLastStep, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Admin{}, rss.ErrNoRecord
	}
	return a, err
}

// Create adds an admin with c, in one transaction with its recovery codes.
// rss.ErrDuplicateEmail if the email is already an admin's.
func (r *AdminRepository) Create(ctx context.Context, email string, c rss.AdminCredentials) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `INSERT INTO admins (email, hashed_password, totp_secret, totp_last_step)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id string
	err = tx.QueryRowContext(ctx, stmt, email, c.HashedPassword, c.SealedTOTPSecret, c.TOTPLastStep).Scan(&id)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", rss.ErrDuplicateEmail
		}
		return "", err
	}
	if err := insertRecoveryCodes(ctx, tx, id, c.RecoveryCodeHashes); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// Reset replaces the password, TOTP secret and recovery codes of the admin
// with email, in one transaction, for an admin who lost their phone or
// password. It also deletes every admin session: a session can't be told
// apart by admin (scs stores its data encoded), and one on a lost or stolen
// device must end. rss.ErrNoRecord if there is no such admin.
func (r *AdminRepository) Reset(ctx context.Context, email string, c rss.AdminCredentials) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `UPDATE admins SET hashed_password = $2, totp_secret = $3, totp_last_step = $4
		WHERE email = $1
		RETURNING id`

	var id string
	err = tx.QueryRowContext(ctx, stmt, email, c.HashedPassword, c.SealedTOTPSecret, c.TOTPLastStep).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.ErrNoRecord
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_recovery_codes WHERE admin_id = $1`, id); err != nil {
		return err
	}
	if err := insertRecoveryCodes(ctx, tx, id, c.RecoveryCodeHashes); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return fmt.Errorf("end admin sessions: %w", err)
	}
	return tx.Commit()
}

func insertRecoveryCodes(ctx context.Context, tx *sql.Tx, adminID string, hashes [][]byte) error {
	stmt := `INSERT INTO admin_recovery_codes (admin_id, hash) VALUES ($1, $2)`
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, stmt, adminID, h); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	return nil
}

// dummyPasswordHash is checked against when there's no admin with the email,
// so Authenticate takes as long whether or not the email is an admin's.
var dummyPasswordHash = sync.OnceValues(func() (string, error) {
	return password.Hash("not an admin's password")
})

// Authenticate returns the admin with email if pw is their password. It
// returns rss.ErrNoAccount if there's no such admin and rss.ErrWrongPassword
// if pw is wrong, after verifying a password either way, so the time taken
// doesn't say which.
func (r *AdminRepository) Authenticate(ctx context.Context, email, pw string) (rss.Admin, error) {
	var hashedPassword []byte
	stmt := `SELECT ` + adminColumns + `, hashed_password FROM admins WHERE email = $1`

	var a rss.Admin
	err := r.DB.QueryRowContext(ctx, stmt, email).Scan(&a.ID, &a.Email, &a.TOTPSecret, &a.TOTPLastStep, &a.CreatedAt, &hashedPassword)
	if errors.Is(err, sql.ErrNoRows) {
		dummy, err := dummyPasswordHash()
		if err != nil {
			return rss.Admin{}, err
		}
		if _, err := password.Verify(pw, dummy); err != nil {
			return rss.Admin{}, err
		}
		return rss.Admin{}, rss.ErrNoAccount
	}
	if err != nil {
		return rss.Admin{}, err
	}

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return rss.Admin{}, fmt.Errorf("verify password for admin %s: %w", a.ID, err)
	}
	if !match {
		return rss.Admin{}, rss.ErrWrongPassword
	}
	return a, nil
}

// Get returns the admin with id, or rss.ErrNoRecord.
func (r *AdminRepository) Get(ctx context.Context, id string) (rss.Admin, error) {
	stmt := `SELECT ` + adminColumns + ` FROM admins WHERE id = $1`
	return scanAdmin(r.DB.QueryRowContext(ctx, stmt, id))
}

// GetByEmail returns the admin with email, or rss.ErrNoRecord.
func (r *AdminRepository) GetByEmail(ctx context.Context, email string) (rss.Admin, error) {
	stmt := `SELECT ` + adminColumns + ` FROM admins WHERE email = $1`
	return scanAdmin(r.DB.QueryRowContext(ctx, stmt, email))
}

// AcceptTOTPStep records that a code for step was accepted for the admin
// with id. It reports false, changing nothing, if a code for step or a later
// one already was, so two requests can't both use one code.
func (r *AdminRepository) AcceptTOTPStep(ctx context.Context, id string, step int64) (bool, error) {
	stmt := `UPDATE admins SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2`
	res, err := r.DB.ExecContext(ctx, stmt, id, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseRecoveryCode deletes the admin's recovery code with hash, so it works
// once. It reports whether there was one.
func (r *AdminRepository) UseRecoveryCode(ctx context.Context, id string, hash []byte) (bool, error) {
	stmt := `DELETE FROM admin_recovery_codes WHERE admin_id = $1 AND hash = $2`
	res, err := r.DB.ExecContext(ctx, stmt, id, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
