package psql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type SiteRepository struct {
	DB *sql.DB
}

func NewSiteRepository(db *sql.DB) *SiteRepository {
	return &SiteRepository{DB: db}
}

// errReservedSite is upsertSite's refusal of the newsletters' internal
// site.
var errReservedSite = errors.New("psql: the newsletters' site is reserved")

// upsertSite inserts the site or updates it by host and returns its ID. An
// empty title or description doesn't overwrite a known one. Only details
// read from the site's home page (fromHome, discovery.Result.HomePage)
// replace an existing site's address, title and description: details read
// from a feed or another page (an article, a Medium author's page) describe
// a new site, but an existing one keeps its own, gaining only a title or
// description it lacks. The newsletters' internal site
// (rss.NewsletterSiteHost) is refused with errReservedSite: discovery never
// produces its host, and nothing from the web may change it or add feeds to
// it.
func upsertSite(ctx context.Context, q querier, s rss.Site, fromHome bool) (string, error) {
	if s.Host == rss.NewsletterSiteHost {
		return "", errReservedSite
	}
	stmt := `INSERT INTO sites (host, url, title, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (host) DO UPDATE SET
			url = CASE WHEN $5 THEN EXCLUDED.url ELSE sites.url END,
			title = CASE WHEN $5 THEN COALESCE(NULLIF(EXCLUDED.title, ''), sites.title)
				ELSE COALESCE(NULLIF(sites.title, ''), EXCLUDED.title) END,
			description = CASE WHEN $5 THEN COALESCE(NULLIF(EXCLUDED.description, ''), sites.description)
				ELSE COALESCE(NULLIF(sites.description, ''), EXCLUDED.description) END,
			updated_at = now()
		RETURNING id`

	var id string
	if err := q.QueryRowContext(ctx, stmt, s.Host, s.URL, s.Title, s.Description, fromHome).Scan(&id); err != nil {
		return "", err
	}

	return id, nil
}

func (r *SiteRepository) GetByID(ctx context.Context, id string) (rss.Site, error) {
	stmt := `SELECT id, host, url, title, description, created_at, updated_at
		FROM sites
		WHERE id = $1`

	return r.get(ctx, stmt, id)
}

func (r *SiteRepository) get(ctx context.Context, stmt string, arg any) (rss.Site, error) {
	var s rss.Site
	err := r.DB.QueryRowContext(ctx, stmt, arg).Scan(&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Site{}, rss.ErrNoRecord
		}
		return rss.Site{}, err
	}

	return s, nil
}

// GetIcon returns the site's stored icon, or rss.ErrNoRecord.
func (r *SiteRepository) GetIcon(ctx context.Context, siteID string) (rss.SiteIcon, error) {
	stmt := `SELECT site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at
		FROM site_icons
		WHERE site_id = $1`

	var icon rss.SiteIcon
	err := r.DB.QueryRowContext(ctx, stmt, siteID).Scan(&icon.SiteID, &icon.SourceURL, &icon.ContentType,
		&icon.Width, &icon.Height, &icon.Data, &icon.Hash, &icon.ETag, &icon.LastModified, &icon.FetchedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.SiteIcon{}, rss.ErrNoRecord
		}
		return rss.SiteIcon{}, err
	}
	return icon, nil
}

// IconHashes returns the icon hash of each of siteIDs that has an icon.
func (r *SiteRepository) IconHashes(ctx context.Context, siteIDs []string) (map[string]string, error) {
	hashes := map[string]string{}
	if len(siteIDs) == 0 {
		return hashes, nil
	}

	stmt := `SELECT site_id, hash FROM site_icons WHERE site_id = ANY($1)`

	rows, err := r.DB.QueryContext(ctx, stmt, pq.Array(siteIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		hashes[id] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return hashes, nil
}

// MatchForSignup matches the sites a person named on the waitlist, at
// signup (#640): one result per candidate, in order. A candidate
// discovery.ParseInput accepts matches the site whose host is its
// discovery.SiteKey; any other matches the site whose title equals it,
// ignoring case and surrounding space, if exactly one does. Only a
// confident match counts, since a wrong one would follow the wrong site.
//
// A matched site's Feed is the one to follow: its only feed that isn't
// gone, or else its one such feed in Discover's catalog; nil when neither
// settles it, since guessing among a site's feeds risks a comments feed. A
// site with no feed that isn't gone has nothing to follow, so it isn't a
// match.
//
// Two statements whatever the number of candidates: the sites, by key
// (the sites_host_key index) and by title (one scan of sites for every
// title candidate, as no index has the lowered titles), then their feeds
// that aren't gone (feeds_site_id_idx).
func (r *SiteRepository) MatchForSignup(ctx context.Context, candidates []string) ([]rss.SignupMatch, error) {
	out := make([]rss.SignupMatch, len(candidates))
	keys := make([]string, len(candidates))
	names := make([]string, len(candidates))
	for i, c := range candidates {
		out[i].Candidate = c
		if u, err := discovery.ParseInput(c); err == nil {
			keys[i] = discovery.SiteKey(u)
		} else {
			names[i] = strings.TrimSpace(c)
		}
	}
	if len(candidates) == 0 {
		return out, nil
	}

	stmt := `WITH c AS (
			SELECT * FROM unnest($1::text[], $2::text[]) WITH ORDINALITY AS c(key, name, n)
		)
		SELECT c.n, s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at
		FROM c JOIN sites s ON s.host = c.key
		WHERE s.host <> '` + rss.NewsletterSiteHost + `'
		UNION ALL
		SELECT n, id, host, url, title, description, created_at, updated_at FROM (
			SELECT c.n, s.*, count(*) OVER (PARTITION BY c.n) AS same
			FROM c JOIN sites s ON lower(btrim(s.title)) = lower(c.name)
			WHERE c.name <> '' AND s.host <> '` + rss.NewsletterSiteHost + `'
		) t WHERE same = 1`

	rows, err := r.DB.QueryContext(ctx, stmt, pq.Array(keys), pq.Array(names))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sites := map[int]*rss.Site{} // by candidate index
	var siteIDs []string
	for rows.Next() {
		var n int
		var s rss.Site
		if err := rows.Scan(&n, &s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sites[n-1] = &s
		siteIDs = append(siteIDs, s.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(siteIDs) == 0 {
		return out, nil
	}

	stmt = `SELECT ` + qualifiedFeedColumns + `, EXISTS (SELECT 1 FROM discover_feeds d WHERE d.feed_id = f.id)
		FROM feeds f
		WHERE f.site_id = ANY($1) AND f.gone_at IS NULL AND f.owner_id IS NULL`

	frows, err := r.DB.QueryContext(ctx, stmt, pq.Array(siteIDs))
	if err != nil {
		return nil, err
	}
	defer frows.Close()

	type siteFeeds struct {
		live, catalog []rss.Feed
	}
	feeds := map[string]*siteFeeds{}
	for frows.Next() {
		var inCatalog bool
		f, err := scanFeed(scanTail{row: frows, tail: []any{&inCatalog}})
		if err != nil {
			return nil, err
		}
		sf := feeds[f.SiteID]
		if sf == nil {
			sf = &siteFeeds{}
			feeds[f.SiteID] = sf
		}
		sf.live = append(sf.live, f)
		if inCatalog {
			sf.catalog = append(sf.catalog, f)
		}
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}

	for i, s := range sites {
		sf := feeds[s.ID]
		if sf == nil {
			continue
		}
		out[i].Site = s
		switch {
		case len(sf.live) == 1:
			out[i].Feed = &sf.live[0]
		case len(sf.catalog) == 1:
			out[i].Feed = &sf.catalog[0]
		}
	}
	return out, nil
}

// siteIconDue is true for a site ClaimIconCheck may claim: its check is
// due, it isn't the newsletters' internal site, and it's never been checked
// or has a subscribed feed. The admin's Overview counts the icon queue with
// it (AdminOverviewRepository.Queues).
const siteIconDue = `icon_next_check_at <= now() AND host <> '` + rss.NewsletterSiteHost + `'
	AND (icon_checked_at IS NULL
	     OR EXISTS (SELECT 1 FROM feeds f JOIN subscriptions s ON s.feed_id = f.id
	                WHERE f.site_id = sites.id))`

// ClaimIconCheck claims a site whose icon check is due (icon_next_check_at
// <= now()) and that has never been checked or has a subscribed feed, by
// moving its icon_next_check_at to now() + lease. It returns the site and its
// current icon, zero if it has none; only the icon's SiteID, SourceURL, ETag
// and LastModified are loaded. If the claimer records nothing, the site is
// due again when the lease runs out. rss.ErrNoRecord when no site is due.
func (r *SiteRepository) ClaimIconCheck(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error) {
	stmt := `WITH claimed AS (
			UPDATE sites SET icon_next_check_at = now() + make_interval(secs => $1)
			WHERE id = (
				SELECT id FROM sites
				WHERE ` + siteIconDue + `
				ORDER BY icon_next_check_at
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, host, url, title, description, created_at, updated_at
		)
		SELECT c.id, c.host, c.url, c.title, c.description, c.created_at, c.updated_at,
			COALESCE(i.site_id::text, ''), COALESCE(i.source_url, ''), COALESCE(i.etag, ''), COALESCE(i.last_modified, '')
		FROM claimed c
		LEFT JOIN site_icons i ON i.site_id = c.id`

	var (
		s    rss.Site
		icon rss.SiteIcon
	)
	err := r.DB.QueryRowContext(ctx, stmt, lease.Seconds()).Scan(
		&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt,
		&icon.SiteID, &icon.SourceURL, &icon.ETag, &icon.LastModified,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Site{}, rss.SiteIcon{}, rss.ErrNoRecord
		}
		return rss.Site{}, rss.SiteIcon{}, err
	}
	return s, icon, nil
}

// SaveIcon stores icon as its site's icon, replacing any previous one, and
// records the check: icon_checked_at = now(), icon_next_check_at = next.
// rss.ErrNoRecord if the site doesn't exist.
func (r *SiteRepository) SaveIcon(ctx context.Context, icon rss.SiteIcon, next time.Time) error {
	stmt := `WITH site AS (
			UPDATE sites SET icon_checked_at = now(), icon_next_check_at = $10
			WHERE id = $1
			RETURNING id
		)
		INSERT INTO site_icons (site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at)
		SELECT id, $2, $3, $4, $5, $6, $7, $8, $9, now() FROM site
		ON CONFLICT (site_id) DO UPDATE SET
			source_url = EXCLUDED.source_url,
			content_type = EXCLUDED.content_type,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			data = EXCLUDED.data,
			hash = EXCLUDED.hash,
			etag = EXCLUDED.etag,
			last_modified = EXCLUDED.last_modified,
			fetched_at = EXCLUDED.fetched_at`

	return execOne(ctx, r.DB, stmt, icon.SiteID, icon.SourceURL, icon.ContentType, icon.Width, icon.Height,
		icon.Data, icon.Hash, icon.ETag, icon.LastModified, next)
}

// RemoveIcon deletes site siteID's icon, if it has one, and records the
// check: icon_checked_at = now(), icon_next_check_at = next. rss.ErrNoRecord
// if the site doesn't exist.
func (r *SiteRepository) RemoveIcon(ctx context.Context, siteID string, next time.Time) error {
	stmt := `WITH removed AS (
			DELETE FROM site_icons WHERE site_id = $1
		)
		UPDATE sites SET icon_checked_at = now(), icon_next_check_at = $2
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, siteID, next)
}

// KeepIcon leaves site siteID's icon as it is and sets its
// icon_next_check_at to next. checked records that the icon was confirmed
// (icon_checked_at = now()), as when it hadn't changed; a failed check
// passes false. rss.ErrNoRecord if the site doesn't exist.
func (r *SiteRepository) KeepIcon(ctx context.Context, siteID string, checked bool, next time.Time) error {
	stmt := `UPDATE sites SET
			icon_checked_at = CASE WHEN $2 THEN now() ELSE icon_checked_at END,
			icon_next_check_at = $3
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, siteID, checked, next)
}

// RekeyResult is what Rekey did.
type RekeyResult struct {
	Skipped                 bool // the version was current
	Feeds, Created, Deleted int
}

// Rekey moves each feed onto the site its key now names, when the stored
// rules version is older than version: a feed whose site is the bare host
// of the key that siteKey gives it (from its site_url, else its url) moves
// to that key's site, created if needed, whose root lookup is queued at the
// seed's priority so the site gets its own details, feeds and icon. A bare
// host's site left with no feeds is deleted. It then records version, all
// in one transaction. As upsertFeed, it never moves a feed between two
// sites with a path or two hosts, except off an alias host's site
// (aliasHosts, discovery.AliasHosts: m.youtube.com, old.reddit.com,
// tumblr.com): a feed there moves whenever its key differs, whatever the
// new key's host, and the site is deleted once it has no feeds. RekeyResult
// says what it did; nothing when the version is current.
func (r *SiteRepository) Rekey(ctx context.Context, version int, siteKey func(siteURL, feedURL string) (key, root string), aliasHosts []string) (RekeyResult, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return RekeyResult{}, err
	}
	defer tx.Rollback()

	// FOR UPDATE: a second deploy's re-key waits, then finds it current.
	var stored int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM site_key_rules FOR UPDATE`).Scan(&stored); err != nil {
		return RekeyResult{}, err
	}
	if stored >= version {
		return RekeyResult{Skipped: true}, nil
	}

	// Only feeds on a bare host's site or an alias host's can move. They're
	// read in full before any write, as a connection runs one statement at
	// a time.
	type feedSite struct{ feedID, url, siteURL, siteID, host string }
	rows, err := tx.QueryContext(ctx, `SELECT f.id, f.url, f.site_url, s.id, s.host
		FROM feeds f JOIN sites s ON s.id = f.site_id
		WHERE position('/' in s.host) = 0 OR split_part(s.host, '/', 1) = ANY($1)`, pq.Array(aliasHosts))
	if err != nil {
		return RekeyResult{}, err
	}
	var feeds []feedSite
	for rows.Next() {
		var f feedSite
		if err := rows.Scan(&f.feedID, &f.url, &f.siteURL, &f.siteID, &f.host); err != nil {
			rows.Close()
			return RekeyResult{}, err
		}
		feeds = append(feeds, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RekeyResult{}, err
	}

	alias := make(map[string]bool, len(aliasHosts))
	for _, h := range aliasHosts {
		alias[h] = true
	}
	var res RekeyResult
	sites := map[string]string{} // key -> site ID, for keys already handled
	emptied := map[string]bool{} // sites that lost a feed
	for _, f := range feeds {
		key, root := siteKey(f.siteURL, f.url)
		if alias[discovery.KeyHost(f.host)] {
			if key == "" || key == f.host {
				continue
			}
		} else if !strings.Contains(key, "/") || discovery.KeyHost(key) != f.host {
			continue
		}
		id, ok := sites[key]
		if !ok {
			created := true
			err := tx.QueryRowContext(ctx, `INSERT INTO sites (host, url) VALUES ($1, $2)
				ON CONFLICT (host) DO NOTHING RETURNING id`, key, root).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				created = false
				err = tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE host = $1`, key).Scan(&id)
			}
			if err != nil {
				return RekeyResult{}, err
			}
			if created {
				res.Created++
			}
			// As the seed list queues one, after readers' lookups.
			if _, err := tx.ExecContext(ctx, `INSERT INTO lookups (site_key, url, priority, home_page)
				VALUES ($1, $2, 1, true) ON CONFLICT (site_key) DO NOTHING`, key, root); err != nil {
				return RekeyResult{}, err
			}
			sites[key] = id
		}
		if _, err := tx.ExecContext(ctx, `UPDATE feeds SET site_id = $2 WHERE id = $1`, f.feedID, id); err != nil {
			return RekeyResult{}, err
		}
		res.Feeds++
		emptied[f.siteID] = true
	}

	if len(emptied) > 0 {
		ids := make([]string, 0, len(emptied))
		for id := range emptied {
			ids = append(ids, id)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM sites s WHERE s.id = ANY($1)
			AND NOT EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id)`, pq.Array(ids))
		if err != nil {
			return RekeyResult{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return RekeyResult{}, err
		}
		res.Deleted = int(n)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE site_key_rules SET version = $1`, version); err != nil {
		return RekeyResult{}, err
	}
	return res, tx.Commit()
}
