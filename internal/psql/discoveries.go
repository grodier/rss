package psql

import (
	"context"
	"database/sql"

	"github.com/grodier/rss/internal/rss"
)

type DiscoveryRepository struct {
	DB *sql.DB
}

func NewDiscoveryRepository(db *sql.DB) *DiscoveryRepository {
	return &DiscoveryRepository{DB: db}
}

// Save upserts the site and its feeds (each with SiteID set to the site's
// ID) in one transaction and returns the site ID.
func (r *DiscoveryRepository) Save(ctx context.Context, site rss.Site, feeds []rss.Feed) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	siteID, err := upsertSite(ctx, tx, site)
	if err != nil {
		return "", err
	}

	for _, f := range feeds {
		f.SiteID = siteID
		if _, err := upsertFeed(ctx, tx, f); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}

	return siteID, nil
}
