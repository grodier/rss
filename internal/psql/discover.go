package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// DiscoverLatest is how many of a catalog feed's newest articles Discover
// shows.
const DiscoverLatest = 3

// DiscoverRepository reads Discover's catalog (discover_feeds) as a reader
// sees it.
type DiscoverRepository struct {
	DB *sql.DB
}

func NewDiscoverRepository(db *sql.DB) *DiscoverRepository {
	return &DiscoverRepository{DB: db}
}

// Sections returns Discover's sections in their order (position), each with
// up to perSection of its catalog feeds that userID doesn't follow, the most
// recently posting first, and the number of such feeds. A section with
// none is left out, as are gone feeds. Each feed comes with its site and
// its DiscoverLatest newest articles.
//
// One statement for every section: the catalog's feeds the reader doesn't
// follow, each with its newest article's time (an index probe on
// articles_feed_sort_idx), numbered and counted per section, then the
// first perSection of each with their newest articles.
func (r *DiscoverRepository) Sections(ctx context.Context, userID string, perSection int) ([]rss.DiscoverSection, error) {
	stmt := `WITH visible AS (
			SELECT d.section_id, f.id, f.site_id, f.url, f.title, f.posts_per_week,
				s.host, s.url AS site_url, s.title AS site_title,
				row_number() OVER w AS n,
				count(*) OVER (PARTITION BY d.section_id) AS total
			FROM discover_feeds d
			JOIN feeds f ON f.id = d.feed_id
			JOIN sites s ON s.id = f.site_id
			LEFT JOIN LATERAL (
				SELECT COALESCE(a.published_at, a.created_at) AS at
				FROM articles a WHERE a.feed_id = f.id
				ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
				LIMIT 1
			) newest ON true
			WHERE f.gone_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.user_id = $1 AND sub.feed_id = f.id)
			WINDOW w AS (PARTITION BY d.section_id ORDER BY newest.at DESC NULLS LAST, f.id)
		)
		SELECT sec.slug, sec.name, v.total, v.id, v.site_id, v.url, v.title, v.posts_per_week,
			v.host, v.site_url, v.site_title,
			l.id, l.title, l.page_title, l.excerpt, l.page_description, l.published_at, l.created_at
		FROM visible v
		JOIN discover_sections sec ON sec.id = v.section_id
		LEFT JOIN LATERAL (
			SELECT a.id, a.title, a.page_title, a.excerpt, a.page_description, a.published_at, a.created_at
			FROM articles a WHERE a.feed_id = v.id
			ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
			LIMIT $3
		) l ON true
		WHERE v.n <= $2
		ORDER BY sec.position, sec.name, v.n, COALESCE(l.published_at, l.created_at) DESC, l.id DESC`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, perSection, DiscoverLatest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rss.DiscoverSection
	for rows.Next() {
		var (
			slug, name                                     string
			total                                          int
			f                                              rss.Feed
			site                                           rss.Site
			articleID, title, pageTitle, excerpt, pageDesc sql.NullString
			published, created                             sql.NullTime
		)
		if err := rows.Scan(&slug, &name, &total, &f.ID, &f.SiteID, &f.Url, &f.Title, &f.PostsPerWeek,
			&site.Host, &site.URL, &site.Title,
			&articleID, &title, &pageTitle, &excerpt, &pageDesc, &published, &created); err != nil {
			return nil, err
		}
		site.ID = f.SiteID

		if len(out) == 0 || out[len(out)-1].Slug != slug {
			out = append(out, rss.DiscoverSection{Name: name, Slug: slug, Total: total})
		}
		sec := &out[len(out)-1]
		if len(sec.Feeds) == 0 || sec.Feeds[len(sec.Feeds)-1].Feed.ID != f.ID {
			sec.Feeds = append(sec.Feeds, rss.DiscoverFeed{Feed: f, Site: site})
		}
		if articleID.Valid {
			feed := &sec.Feeds[len(sec.Feeds)-1]
			feed.Latest = append(feed.Latest, rss.Article{
				ID:              articleID.String,
				FeedID:          f.ID,
				Title:           title.String,
				PageTitle:       pageTitle.String,
				Excerpt:         excerpt.String,
				PageDescription: pageDesc.String,
				PublishedAt:     nullTime(published),
				CreatedAt:       nullTime(created),
			})
		}
	}
	return out, rows.Err()
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
