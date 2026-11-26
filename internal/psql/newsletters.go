package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// NewsletterRepository stores readers' newsletter addresses
// (newsletter_addresses rows).
type NewsletterRepository struct {
	DB *sql.DB
}

func NewNewsletterRepository(db *sql.DB) *NewsletterRepository {
	return &NewsletterRepository{DB: db}
}

const newsletterAddressColumns = `id, user_id, local_part, created_at, retired_at`

func scanNewsletterAddress(row interface{ Scan(...any) error }) (rss.NewsletterAddress, error) {
	var a rss.NewsletterAddress
	var retired sql.NullTime
	err := row.Scan(&a.ID, &a.UserID, &a.LocalPart, &a.CreatedAt, &retired)
	a.RetiredAt = retired.Time
	return a, err
}

// CurrentAddress returns the user's current address, or rss.ErrNoRecord if
// they haven't got one.
func (r *NewsletterRepository) CurrentAddress(ctx context.Context, userID string) (rss.NewsletterAddress, error) {
	return currentAddress(ctx, r.DB, userID)
}

func currentAddress(ctx context.Context, q querier, userID string) (rss.NewsletterAddress, error) {
	stmt := `SELECT ` + newsletterAddressColumns + ` FROM newsletter_addresses
		WHERE user_id = $1 AND retired_at IS NULL`
	a, err := scanNewsletterAddress(q.QueryRowContext(ctx, stmt, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.NewsletterAddress{}, rss.ErrNoRecord
	}
	return a, err
}

// NewAddress makes localPart the user's current address, retiring the one
// they had, which keeps receiving for rss.NewsletterRetiredFor. It refuses
// with rss.ErrAddressTooRecent when the current address is less than
// rss.NewsletterAddressEvery old, and with rss.ErrDuplicateAddress when
// another address (any reader's, current or retired) has localPart,
// ignoring case. rss.ErrNoRecord if there's no such user.
//
// The user's row is locked first, so two requests at once make one address
// and the other is refused as too recent.
func (r *NewsletterRepository) NewAddress(ctx context.Context, userID, localPart string) (rss.NewsletterAddress, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.NewsletterAddress{}, err
	}
	defer tx.Rollback()

	if _, err := lockUser(ctx, tx, userID); err != nil {
		return rss.NewsletterAddress{}, err
	}

	// The database's clock decides, as it set created_at.
	var curID string
	var tooRecent bool
	err = tx.QueryRowContext(ctx, `SELECT id, created_at > now() - make_interval(secs => $2)
		FROM newsletter_addresses WHERE user_id = $1 AND retired_at IS NULL`,
		userID, rss.NewsletterAddressEvery.Seconds()).Scan(&curID, &tooRecent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return rss.NewsletterAddress{}, err
	case tooRecent:
		return rss.NewsletterAddress{}, rss.ErrAddressTooRecent
	default:
		if err := execOne(ctx, tx, `UPDATE newsletter_addresses SET retired_at = now() WHERE id = $1`, curID); err != nil {
			return rss.NewsletterAddress{}, err
		}
	}

	stmt := `INSERT INTO newsletter_addresses (user_id, local_part) VALUES ($1, $2)
		RETURNING ` + newsletterAddressColumns
	a, err := scanNewsletterAddress(tx.QueryRowContext(ctx, stmt, userID, localPart))
	if err != nil {
		if isUniqueViolation(err) {
			return rss.NewsletterAddress{}, rss.ErrDuplicateAddress
		}
		return rss.NewsletterAddress{}, err
	}
	if err := tx.Commit(); err != nil {
		return rss.NewsletterAddress{}, err
	}
	return a, nil
}
