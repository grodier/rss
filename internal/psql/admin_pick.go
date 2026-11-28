package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// adminPickCandidate is the condition that feed f is still to decide on
// the picking page: not gone, not in Discover's catalog, not skipped.
const adminPickCandidate = `f.gone_at IS NULL AND f.owner_id IS NULL
	AND NOT EXISTS (SELECT 1 FROM discover_feeds cd WHERE cd.feed_id = f.id)
	AND NOT EXISTS (SELECT 1 FROM discover_skips ck WHERE ck.feed_id = f.id)`

// adminPickSkipped is the condition that feed f is skipped and not gone:
// a gone feed unskipped wouldn't be back in the queue.
const adminPickSkipped = `f.gone_at IS NULL AND f.owner_id IS NULL AND EXISTS (SELECT 1 FROM discover_skips ck WHERE ck.feed_id = f.id)`

// adminPickSeeded selects, for each site a seed entry's lookup found, its
// newest such entry (by added_at, then site key) as seeded: the seed
// filter's order and the site's section hint and source.
const adminPickSeeded = `seeded AS (
		SELECT DISTINCT ON (l.site_id) l.site_id, ss.added_at, ss.site_key, ss.section_id, ss.source
		FROM seed_sites ss JOIN lookups l ON l.site_key = ss.site_key
		WHERE l.site_id IS NOT NULL
		ORDER BY l.site_id, ss.added_at DESC, ss.site_key DESC
	)`

// adminPickComments is the condition that feed f is a comments feed: its
// title has the word "comment" or "comments", or a segment of its address
// is one, so "Commentary" isn't.
const adminPickComments = `(f.title ~* '\mcomments?\M' OR f.url ~* '[/._-]comments?([/._?-]|$)')`

// adminPickFeeds is how many of a site's feeds the picking page lists.
const adminPickFeeds = 20

// adminPickLatest is how many of a feed's newest titles the picking page
// shows.
const adminPickLatest = 3

// CountPick counts the sites each of the picking page's filters lists, by
// filter: those with a candidate feed found by a seed entry's lookup, all
// those with a candidate feed, and those with a skipped feed.
func (r *AdminFeedRepository) CountPick(ctx context.Context) (map[string]int, error) {
	stmt := `WITH candidates AS (
			SELECT DISTINCT f.site_id FROM feeds f WHERE ` + adminPickCandidate + `
		)
		SELECT
			(SELECT count(*) FROM candidates c WHERE EXISTS (SELECT 1 FROM seed_sites ss
				JOIN lookups l ON l.site_key = ss.site_key WHERE l.site_id = c.site_id)),
			(SELECT count(*) FROM candidates),
			(SELECT count(DISTINCT f.site_id) FROM discover_skips k JOIN feeds f ON f.id = k.feed_id WHERE f.gone_at IS NULL AND f.owner_id IS NULL)`
	var seeded, all, skipped int
	if err := r.DB.QueryRowContext(ctx, stmt).Scan(&seeded, &all, &skipped); err != nil {
		return nil, err
	}
	return map[string]int{rss.AdminPickSeed: seeded, rss.AdminPickAll: all, rss.AdminPickSkipped: skipped}, nil
}

// PickSites returns a page of the picking page's sites under q's filter,
// starting after the site q.After: under Seed, the sites a seed entry's
// lookup found that have a candidate feed, by their newest entry, newest
// first; under All, every site with a candidate feed, newest site first;
// under Skipped, every site with a skipped feed, in All's order. Each comes
// with its seed entry's source and section hint, else the section of its
// feed already in the catalog, and up to adminPickFeeds of its candidate
// (or skipped) feeds, most recently posting first and comments feeds last,
// with how many more it has. An unknown filter is an error. Each view
// looks for candidates across all feeds (see "The picking page looks for
// candidate feeds across all feeds" in docs/backlog.md).
func (r *AdminFeedRepository) PickSites(ctx context.Context, q rss.AdminPickQuery) ([]rss.AdminPickSite, error) {
	feedCond := adminPickCandidate
	var from, order, cursor string
	switch q.Filter {
	case rss.AdminPickSeed:
		from = `seeded sd JOIN sites s ON s.id = sd.site_id`
		order = `sd.added_at DESC, sd.site_key DESC`
		cursor = `(sd.added_at, sd.site_key) < (SELECT c.added_at, c.site_key FROM seeded c WHERE c.site_id::text = $2)`
	case rss.AdminPickSkipped:
		feedCond = adminPickSkipped
		fallthrough
	case rss.AdminPickAll:
		from = `sites s LEFT JOIN seeded sd ON sd.site_id = s.id`
		order = `s.created_at DESC, s.id DESC`
		cursor = `(s.created_at, s.id) < (SELECT c.created_at, c.id FROM sites c WHERE c.id::text = $2)`
	default:
		return nil, fmt.Errorf("unknown pick filter %q", q.Filter)
	}
	stmt := `WITH ` + adminPickSeeded + `
		SELECT s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at,
			COALESCE(sd.source, ''), COALESCE(hs.id::text, ''), COALESCE(hs.slug, ''), COALESCE(hs.name, ''),
			COALESCE(cs.id::text, ''), COALESCE(cs.slug, ''), COALESCE(cs.name, '')
		FROM ` + from + `
		LEFT JOIN discover_sections hs ON hs.id = sd.section_id
		LEFT JOIN LATERAL (
			SELECT ds.id, ds.slug, ds.name FROM feeds cf
			JOIN discover_feeds d ON d.feed_id = cf.id
			JOIN discover_sections ds ON ds.id = d.section_id
			WHERE cf.site_id = s.id
			ORDER BY ds.position, ds.name
			LIMIT 1
		) cs ON true
		WHERE EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id AND ` + feedCond + `)
			AND ($2 = '' OR ` + cursor + `)
		ORDER BY ` + order + `
		LIMIT $1`
	rows, err := r.DB.QueryContext(ctx, stmt, q.Limit, q.After)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminPickSite
	index := map[string]int{}
	var ids []string
	for rows.Next() {
		var p rss.AdminPickSite
		var catalog rss.Section
		s := &p.Site
		if err := rows.Scan(&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt,
			&p.Source, &p.Hint.ID, &p.Hint.Slug, &p.Hint.Name, &catalog.ID, &catalog.Slug, &catalog.Name); err != nil {
			return nil, err
		}
		p.HintSeed = p.Hint.ID != ""
		if !p.HintSeed {
			p.Hint = catalog
		}
		index[s.ID] = len(out)
		ids = append(ids, s.ID)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	feeds := `WITH listed AS (
			SELECT f.id, f.site_id, f.title, f.url, f.posts_per_week, f.categories, newest.at, ` + adminPickComments + ` AS comments,
				row_number() OVER (PARTITION BY f.site_id
					ORDER BY ` + adminPickComments + `, newest.at DESC NULLS LAST, f.id) AS n,
				count(*) OVER (PARTITION BY f.site_id) AS total
			FROM feeds f
			LEFT JOIN LATERAL (` + newestArticle + `) newest ON true
			WHERE f.site_id = ANY($1::uuid[]) AND ` + feedCond + `
		)
		SELECT v.site_id, v.id, v.title, v.url, v.posts_per_week, v.categories, v.at, v.comments, v.total,
			ARRAY(SELECT COALESCE(NULLIF(a.title, ''), a.page_title) FROM articles a WHERE a.feed_id = v.id
				ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC LIMIT $3)
		FROM listed v
		WHERE v.n <= $2
		ORDER BY v.site_id, v.n`
	frows, err := r.DB.QueryContext(ctx, feeds, pq.Array(ids), adminPickFeeds, adminPickLatest)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var siteID string
		var f rss.AdminPickFeed
		var latest sql.NullTime
		var total int
		if err := frows.Scan(&siteID, &f.ID, &f.Title, &f.URL, &f.PostsPerWeek, pq.Array(&f.Categories), &latest, &f.Comments, &total,
			pq.Array(&f.Latest)); err != nil {
			return nil, err
		}
		f.LatestPost = latest.Time
		p := &out[index[siteID]]
		p.Feeds = append(p.Feeds, f)
		p.More = total - min(total, adminPickFeeds)
	}
	return out, frows.Err()
}

// SavePicks applies the picking page's Save in one transaction: it puts
// each of picks in the catalog as SetDiscoverSection does, recorded as
// feed.discover, then skips each of skips that isn't in the catalog,
// recorded as feed.discover_skip (a feed already skipped is left as it
// is, and a reader's newsletter feed is never skipped). A pick that fails saves nothing and returns *rss.PickError naming
// the feed, wrapping rss.ErrUnknownSection, rss.ErrFeedGone or
// rss.ErrNoRecord.
func (r *AdminFeedRepository) SavePicks(ctx context.Context, picks []rss.DiscoverPick, skips []string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// In ID order, so two saves of the same feeds lock them in the same
	// order rather than deadlock.
	picks = slices.SortedFunc(slices.Values(picks), func(a, b rss.DiscoverPick) int { return strings.Compare(a.FeedID, b.FeedID) })
	for _, p := range picks {
		err := setDiscoverSection(ctx, tx, p.FeedID, p.SectionID, by)
		if errors.Is(err, rss.ErrUnknownSection) || errors.Is(err, rss.ErrFeedGone) || errors.Is(err, rss.ErrNoRecord) {
			return &rss.PickError{FeedID: p.FeedID, Err: err}
		}
		if err != nil {
			return err
		}
	}
	if len(skips) > 0 {
		rows, err := tx.QueryContext(ctx, `INSERT INTO discover_skips (feed_id, skipped_by)
			SELECT f.id, NULLIF($2, '')::uuid FROM feeds f
			WHERE f.id = ANY($1::uuid[]) AND f.owner_id IS NULL
			  AND NOT EXISTS (SELECT 1 FROM discover_feeds d WHERE d.feed_id = f.id)
			ORDER BY f.id
			ON CONFLICT (feed_id) DO NOTHING
			RETURNING feed_id`, pq.Array(skips), by.AdminID)
		if err != nil {
			return err
		}
		var skipped []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			skipped = append(skipped, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range skipped {
			err := recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "feed.discover_skip", TargetType: "feed", TargetID: id})
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Unskip puts the skipped feed id back in the picking page's queue,
// recorded as feed.discover_unskip by by. rss.ErrNoRecord when it isn't
// skipped.
func (r *AdminFeedRepository) Unskip(ctx context.Context, id string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unskip(ctx, tx, id, by); err != nil {
		return err
	}
	return tx.Commit()
}

// unskip deletes feed id's skip in tx and records feed.discover_unskip by
// by; rss.ErrNoRecord when it isn't skipped.
func unskip(ctx context.Context, tx *sql.Tx, id string, by rss.AuditActor) error {
	if err := execOne(ctx, tx, `DELETE FROM discover_skips WHERE feed_id = $1`, id); err != nil {
		return err
	}
	return recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "feed.discover_unskip", TargetType: "feed", TargetID: id})
}
