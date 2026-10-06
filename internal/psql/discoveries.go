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

// Save upserts the site, its feeds (each with SiteID set to the site's ID)
// and each feed's articles (see saveArticles, which also sets the feed's
// last_fetched_at) in one transaction, and returns the site ID.
func (r *DiscoveryRepository) Save(ctx context.Context, site rss.Site, feeds []rss.FeedWithArticles) (string, error) {
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
		f.Feed.SiteID = siteID
		id, err := upsertFeed(ctx, tx, f.Feed)
		if err != nil {
			return "", err
		}
		if _, err := saveArticles(ctx, tx, id, f.Articles); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}

	return siteID, nil
}
