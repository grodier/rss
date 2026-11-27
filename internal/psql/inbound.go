package psql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// InboundRepository stores the emails POST /inbound/email receives
// (inbound_emails rows) for the newsletter runner.
type InboundRepository struct {
	DB *sql.DB
}

func NewInboundRepository(db *sql.DB) *InboundRepository {
	return &InboundRepository{DB: db}
}

// ResolveAddress returns the address with localPart (ignoring case), and its
// reader, if it's a current address or one retired within
// rss.NewsletterRetiredFor. It returns rss.ErrNoRecord for an unknown or
// expired address, and rss.ErrNoAccess when the reader hasn't got
// rss.FeatureNewsletters.
func (r *InboundRepository) ResolveAddress(ctx context.Context, localPart string) (addressID, userID string, err error) {
	var access bool
	err = r.DB.QueryRowContext(ctx, `SELECT a.id, a.user_id,
			EXISTS (SELECT 1 FROM user_features f WHERE f.user_id = a.user_id AND f.feature = $3)
		FROM newsletter_addresses a
		WHERE a.local_part = $1
		  AND (a.retired_at IS NULL OR a.retired_at > now() - make_interval(secs => $2))`,
		localPart, rss.NewsletterRetiredFor.Seconds(), string(rss.FeatureNewsletters)).Scan(&addressID, &userID, &access)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", rss.ErrNoRecord
	case err != nil:
		return "", "", err
	case !access:
		return "", "", rss.ErrNoAccess
	}
	return addressID, userID, nil
}

// Store saves raw, received for addressID, reader userID's address, for the
// newsletter runner. It refuses with rss.ErrInboundFull when it would take
// the reader's waiting emails over rss.InboundPendingMaxBytes, and with
// rss.ErrDuplicateInbound when the same email is already waiting for the
// address (a replay). rss.ErrNoRecord if there's no such user.
//
// The user's row is locked first, so two emails at once can't both fit
// under the cap.
func (r *InboundRepository) Store(ctx context.Context, addressID, userID string, raw []byte) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := lockUser(ctx, tx, userID); err != nil {
		return err
	}

	var pending int64
	err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(octet_length(e.raw)), 0)
		FROM inbound_emails e JOIN newsletter_addresses a ON a.id = e.address_id
		WHERE a.user_id = $1`, userID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending+int64(len(raw)) > rss.InboundPendingMaxBytes {
		return rss.ErrInboundFull
	}

	digest := sha256.Sum256(raw)
	_, err = tx.ExecContext(ctx, `INSERT INTO inbound_emails (address_id, raw, digest) VALUES ($1, $2, $3)`,
		addressID, raw, digest[:])
	if err != nil {
		if isUniqueViolation(err) {
			return rss.ErrDuplicateInbound
		}
		return err
	}
	return tx.Commit()
}
