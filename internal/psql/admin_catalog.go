package psql

import (
	"context"
	"database/sql"

	"github.com/grodier/rss/internal/rss"
)

// Catalog returns Discover's sections in their default order, each with
// its catalog feeds by title, empty sections included, and gone feeds,
// which Discover hides, too, so the admin can take them out. The catalog is
// small, edited by hand, so each feed's latest article and subscribers are
// counted per row.
func (r *AdminFeedRepository) Catalog(ctx context.Context) ([]rss.AdminCatalogSection, error) {
	stmt := `SELECT ds.id, ds.slug, ds.name, COALESCE(f.id::text, ''), COALESCE(f.title, ''), COALESCE(f.url, ''),
			COALESCE(s.id::text, ''), COALESCE(s.host, ''), COALESCE(f.posts_per_week, 0),
			(SELECT max(COALESCE(a.published_at, a.created_at)) FROM articles a WHERE a.feed_id = f.id),
			CASE WHEN f.id IS NULL THEN 0 ELSE ` + adminSubscribers("f") + ` END, f.gone_at
		FROM discover_sections ds
		LEFT JOIN discover_feeds d ON d.section_id = ds.id
		LEFT JOIN feeds f ON f.id = d.feed_id
		LEFT JOIN sites s ON s.id = f.site_id
		ORDER BY ds.position, ds.name, lower(f.title), f.url`
	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminCatalogSection
	for rows.Next() {
		var sec rss.Section
		var f rss.AdminCatalogFeed
		var latest, gone sql.NullTime
		if err := rows.Scan(&sec.ID, &sec.Slug, &sec.Name, &f.ID, &f.Title, &f.URL,
			&f.SiteID, &f.SiteHost, &f.PostsPerWeek, &latest, &f.Subscribers, &gone); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].ID != sec.ID {
			out = append(out, rss.AdminCatalogSection{Section: sec})
		}
		if f.ID != "" {
			f.LatestPost, f.GoneAt = latest.Time, gone.Time
			last := &out[len(out)-1]
			last.Feeds = append(last.Feeds, f)
		}
	}
	return out, rows.Err()
}
