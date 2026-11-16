package psql

import (
	"context"
	"database/sql"
	"errors"
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

// Sections returns Discover's sections in userID's order, each with up to
// perSection of its catalog feeds that userID doesn't follow, the most
// recently posting first, and the number of catalog feeds it has, followed
// or not (Total, for "All N", which the section's page lists). A section
// with no feed the reader doesn't follow is left out, as are gone feeds.
// Each feed comes with its site and its DiscoverLatest newest articles.
//
// The reader's order: the sections with the most catalog feeds they follow
// first, then, among equals, the sections they chose on the waitlist
// (users.sections, in its order), then the default order (position).
//
// One statement for every section: the catalog's feeds, each with its
// newest article's time (an index probe on articles_feed_sort_idx) and
// whether the reader follows it (a probe of the subscriptions key), counted
// per section, then those the reader doesn't follow numbered per section,
// then the first perSection of each with their newest articles.
func (r *DiscoverRepository) Sections(ctx context.Context, userID string, perSection int) ([]rss.DiscoverSection, error) {
	stmt := `WITH catalog AS (
			SELECT d.section_id, f.id, f.site_id, f.url, f.title, f.posts_per_week,
				s.host, s.url AS site_url, s.title AS site_title, newest.at,
				sub.feed_id IS NOT NULL AS following,
				count(*) OVER (PARTITION BY d.section_id) AS total,
				count(sub.feed_id) OVER (PARTITION BY d.section_id) AS followed
			FROM discover_feeds d
			JOIN feeds f ON f.id = d.feed_id
			JOIN sites s ON s.id = f.site_id
			LEFT JOIN subscriptions sub ON sub.user_id = $1 AND sub.feed_id = f.id
			LEFT JOIN LATERAL (` + newestArticle + `) newest ON true
			WHERE f.gone_at IS NULL
		), visible AS (
			SELECT c.*, row_number() OVER (PARTITION BY c.section_id ORDER BY c.at DESC NULLS LAST, c.id) AS n
			FROM catalog c
			WHERE NOT c.following
		)
		SELECT sec.slug, sec.name, sec.position, v.total, v.id, v.site_id, v.url, v.title, v.posts_per_week,
			v.host, v.site_url, v.site_title,
			` + latestColumns + `
		FROM visible v
		JOIN discover_sections sec ON sec.id = v.section_id
		LEFT JOIN users u ON u.id = $1
		LEFT JOIN LATERAL (` + latestArticles("v.id", "$3") + `) l ON true
		WHERE v.n <= $2
		ORDER BY v.followed DESC, array_position(u.sections, sec.id) NULLS LAST, sec.position, sec.name,
			v.n, COALESCE(l.published_at, l.created_at) DESC, l.id DESC`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, perSection, DiscoverLatest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rss.DiscoverSection
	for rows.Next() {
		var (
			slug, name      string
			position, total int
			f               rss.DiscoverFeed
			l               latestArticle
		)
		dests := append([]any{&slug, &name, &position, &total}, discoverFeedDests(&f)...)
		if err := rows.Scan(append(dests, l.dests()...)...); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Slug != slug {
			out = append(out, rss.DiscoverSection{Name: name, Slug: slug, Position: position, Total: total})
		}
		sec := &out[len(out)-1]
		sec.Feeds = appendLatest(sec.Feeds, f, l)
	}
	return out, rows.Err()
}

// ListSections returns Discover's sections, without their feeds, in their
// default order.
func (r *DiscoverRepository) ListSections(ctx context.Context) ([]rss.Section, error) {
	return listSections(ctx, r.DB)
}

// listSections returns every discover_sections row in the default order.
func listSections(ctx context.Context, db *sql.DB) ([]rss.Section, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, slug, name FROM discover_sections ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.Section
	for rows.Next() {
		var sec rss.Section
		if err := rows.Scan(&sec.ID, &sec.Slug, &sec.Name); err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// Section returns the section with slug, or rss.ErrNoRecord.
func (r *DiscoverRepository) Section(ctx context.Context, slug string) (rss.Section, error) {
	var sec rss.Section
	err := r.DB.QueryRowContext(ctx, `SELECT id, slug, name FROM discover_sections WHERE slug = $1`, slug).
		Scan(&sec.ID, &sec.Slug, &sec.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Section{}, rss.ErrNoRecord
	}
	return sec, err
}

// SectionFeeds returns every catalog feed in the section sectionID that
// isn't gone, the most recently posting first, each with its site, whether
// userID follows it and its DiscoverLatest newest articles.
func (r *DiscoverRepository) SectionFeeds(ctx context.Context, userID, sectionID string) ([]rss.DiscoverFeed, error) {
	stmt := `SELECT f.id, f.site_id, f.url, f.title, f.posts_per_week,
			s.host, s.url, s.title,
			EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.user_id = $1 AND sub.feed_id = f.id),
			` + latestColumns + `
		FROM discover_feeds d
		JOIN feeds f ON f.id = d.feed_id
		JOIN sites s ON s.id = f.site_id
		LEFT JOIN LATERAL (` + newestArticle + `) newest ON true
		LEFT JOIN LATERAL (` + latestArticles("f.id", "$3") + `) l ON true
		WHERE d.section_id = $2 AND f.gone_at IS NULL
		ORDER BY newest.at DESC NULLS LAST, f.id, COALESCE(l.published_at, l.created_at) DESC, l.id DESC`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, sectionID, DiscoverLatest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rss.DiscoverFeed
	for rows.Next() {
		var (
			f rss.DiscoverFeed
			l latestArticle
		)
		dests := append(discoverFeedDests(&f), &f.Following)
		if err := rows.Scan(append(dests, l.dests()...)...); err != nil {
			return nil, err
		}
		out = appendLatest(out, f, l)
	}
	return out, rows.Err()
}

// SectionPosts returns up to limit articles of the section sectionID's
// catalog feeds that aren't gone, newest first (by published time, else
// the time it was saved, like a feed page), after before (from the start
// if it's zero), each with its feed, site and whether userID follows it.
//
// For each catalog feed, its newest limit articles before the cursor from
// articles_feed_sort_idx, then merged, keeping limit: the section's feeds
// bound the work, not its articles.
func (r *DiscoverRepository) SectionPosts(ctx context.Context, userID, sectionID string, before rss.ArticleCursor, limit int) ([]rss.SectionPost, error) {
	stmt := `SELECT ` + qualifiedArticleColumns + `,
			f.id, f.site_id, f.url, f.title, f.posts_per_week,
			s.host, s.url, s.title,
			EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.user_id = $1 AND sub.feed_id = f.id)
		FROM discover_feeds d
		JOIN feeds f ON f.id = d.feed_id
		JOIN sites s ON s.id = f.site_id
		CROSS JOIN LATERAL (
			SELECT ` + qualifiedListArticleColumns + `
			FROM articles a
			WHERE a.feed_id = f.id
				AND ($3::timestamptz IS NULL OR (COALESCE(a.published_at, a.created_at), a.id) < ($3, $4::uuid))
			ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
			LIMIT $5
		) a
		WHERE d.section_id = $2 AND f.gone_at IS NULL
		ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
		LIMIT $5`

	at, id := cursorArgs(before)
	rows, err := r.DB.QueryContext(ctx, stmt, userID, sectionID, at, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []rss.SectionPost{}
	for rows.Next() {
		var p rss.SectionPost
		p.Article, err = scanArticle(scanFunc(func(articleDests ...any) error {
			dests := append(articleDests, discoverFeedDests(&p.Feed)...)
			return rows.Scan(append(dests, &p.Feed.Following)...)
		}))
		if err != nil {
			return nil, err
		}
		p.Feed.Site.ID = p.Feed.Feed.SiteID
		out = append(out, p)
	}
	return out, rows.Err()
}

// newestArticle selects the time of the newest article of the feed f, as
// at, for a LATERAL join: one probe of articles_feed_sort_idx.
const newestArticle = `SELECT COALESCE(a.published_at, a.created_at) AS at
				FROM articles a WHERE a.feed_id = f.id
				ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
				LIMIT 1`

// latestArticles selects the newest limit articles of the feed feedID, as
// latestColumns reads them, for a LATERAL join aliased l.
func latestArticles(feedID, limit string) string {
	return `SELECT a.id, a.title, a.page_title, a.excerpt, a.page_description, a.published_at, a.created_at
			FROM articles a WHERE a.feed_id = ` + feedID + `
			ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
			LIMIT ` + limit
}

// latestColumns are a latestArticles row's columns, in latestArticle's
// order; NULL when the feed has no articles.
const latestColumns = `l.id, l.title, l.page_title, l.excerpt, l.page_description, l.published_at, l.created_at`

// latestArticle is a latestColumns row: one of a feed's newest articles.
type latestArticle struct {
	id, title, pageTitle, excerpt, pageDesc sql.NullString
	published, created                      sql.NullTime
}

func (l *latestArticle) dests() []any {
	return []any{&l.id, &l.title, &l.pageTitle, &l.excerpt, &l.pageDesc, &l.published, &l.created}
}

// discoverFeedDests are the destinations of a catalog feed's columns: the
// feed's id, site_id, url, title and posts_per_week, then its site's host,
// url and title.
func discoverFeedDests(f *rss.DiscoverFeed) []any {
	return []any{&f.Feed.ID, &f.Feed.SiteID, &f.Feed.Url, &f.Feed.Title, &f.Feed.PostsPerWeek,
		&f.Site.Host, &f.Site.URL, &f.Site.Title}
}

// appendLatest adds l to the newest articles of the last of feeds when it's
// f, else appends f first: a feed's rows are consecutive, one per article.
func appendLatest(feeds []rss.DiscoverFeed, f rss.DiscoverFeed, l latestArticle) []rss.DiscoverFeed {
	f.Site.ID = f.Feed.SiteID
	if len(feeds) == 0 || feeds[len(feeds)-1].Feed.ID != f.Feed.ID {
		feeds = append(feeds, f)
	}
	if l.id.Valid {
		feed := &feeds[len(feeds)-1]
		feed.Latest = append(feed.Latest, rss.Article{
			ID:              l.id.String,
			FeedID:          f.Feed.ID,
			Title:           l.title.String,
			PageTitle:       l.pageTitle.String,
			Excerpt:         l.excerpt.String,
			PageDescription: l.pageDesc.String,
			PublishedAt:     nullTime(l.published),
			CreatedAt:       nullTime(l.created),
		})
	}
	return feeds
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
