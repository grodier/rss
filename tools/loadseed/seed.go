package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/lib/pq"
)

type seedConfig struct {
	prefix        string
	feeds         int
	subscriptions int
	years         float64
	seed          int64
}

// The seeded users' password, so the seed can also be browsed in the app.
const seedPassword = "loadseed-password"

// Shares of the seeded feeds by posting rate; the rest are weekly.
const (
	busyShare  = 0.02 // 20–50 articles a day
	dailyShare = 0.20
)

const (
	mainBusySubscriptions = 10 // busy feeds among the main user's subscriptions
	smallSubscriptions    = 30 // the baseline user's subscriptions
	starCount             = 2000
	tagCount              = 10
	copyShare             = 0.025 // articles copied into another feed, so about 5% share a canonical URL
	mediaShare            = 0.02
	rareWordShare         = 0.01 // articles whose title has one of rareWords
)

// commonWords make up the titles and excerpts.
var commonWords = []string{
	"the", "a", "of", "and", "to", "in", "is", "for", "on", "with", "new", "how", "why", "what", "your", "our",
	"release", "update", "notes", "week", "year", "guide", "review", "first", "look", "inside", "building", "making",
	"design", "garden", "kitchen", "music", "city", "travel", "science", "history", "code", "data", "market", "policy",
	"health", "sleep", "coffee", "bread", "river", "mountain", "winter", "summer", "spring", "autumn", "photo", "story",
	"interview", "podcast", "episode", "letter", "essay", "report", "small", "large", "quiet", "simple", "better",
	"faster", "slower", "older", "modern", "local", "public", "open", "future", "past", "today", "tomorrow", "night",
	"morning", "evening", "people", "teams", "tools", "systems", "habits", "notes", "ideas", "questions", "answers",
	"lessons", "mistakes", "changes", "plans", "maps", "books", "films", "games", "streets", "houses", "bridges",
}

// rareWords appear in about rareWordShare of titles; the Words timeline
// includes them.
var rareWords = []string{"quasar", "zeppelin", "marzipan"}

// Names of the main user's custom timelines.
const (
	timelineTagOnly = "Tag only"
	timelineWords   = "Words"
	timelineQuiet   = "Quiet posters"
	timelineMedia   = "Media"
	timelineStarred = "Starred"
)

// seedTables are the tables a seed writes to.
const seedTables = `sites, feeds, articles, users, subscriptions, tags, subscription_tags, stars, timelines, timeline_tags, timeline_feeds`

func mainEmail(prefix string) string  { return prefix + "@example.com" }
func smallEmail(prefix string) string { return prefix + "-small@example.com" }

// hostPattern matches the hosts of prefix's sites: <prefix>-<n>.example.com.
// The digits keep a prefix from matching a longer one's sites ("loadseed"
// against "loadseed-test-…").
func hostPattern(prefix string) string { return `^` + prefix + `-[0-9]+\.example\.com$` }

// feedKind is a seeded feed's posting rate.
type feedKind int

const (
	busy feedKind = iota
	daily
	weekly
)

// feedPlan is one feed to seed.
type feedPlan struct {
	host     string
	kind     feedKind
	perDay   float64
	articles int
	id       string // set once inserted
}

// planFeeds returns cfg.feeds feeds: the busy ones first, then the daily
// ones, then the weekly ones, with at least one busy and one daily feed
// when there's room.
func planFeeds(cfg seedConfig, rng *rand.Rand) []feedPlan {
	nBusy := max(1, int(math.Round(float64(cfg.feeds)*busyShare)))
	nDaily := min(cfg.feeds-nBusy, max(1, int(math.Round(float64(cfg.feeds)*dailyShare))))
	days := cfg.years * 365

	feeds := make([]feedPlan, cfg.feeds)
	for i := range feeds {
		f := &feeds[i]
		f.host = fmt.Sprintf("%s-%d.example.com", cfg.prefix, i+1)
		switch {
		case i < nBusy:
			f.kind, f.perDay = busy, float64(20+rng.IntN(31))
		case i < nBusy+nDaily:
			f.kind, f.perDay = daily, 1
		default:
			f.kind, f.perDay = weekly, 1.0/7
		}
		f.articles = max(1, int(math.Round(f.perDay*days)))
	}
	return feeds
}

// cleanup deletes what an earlier seed with prefix made: its users and
// sites, and through them (ON DELETE CASCADE) their feeds, articles,
// subscriptions, tags, stars and timelines.
func cleanup(ctx context.Context, db *sql.DB, prefix string) (users, sites int64, err error) {
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE email IN ($1, $2)`, mainEmail(prefix), smallEmail(prefix))
	if err != nil {
		return 0, 0, fmt.Errorf("delete users: %w", err)
	}
	users, _ = res.RowsAffected()
	res, err = db.ExecContext(ctx, `DELETE FROM sites WHERE host ~ $1`, hostPattern(prefix))
	if err != nil {
		return 0, 0, fmt.Errorf("delete sites: %w", err)
	}
	sites, _ = res.RowsAffected()
	return users, sites, nil
}

// seed deletes an earlier seed with cfg.prefix, seeds a new one and
// ANALYZEs the tables, printing what it did to out.
func seed(ctx context.Context, db *sql.DB, cfg seedConfig, out io.Writer) error {
	start := time.Now()
	users, sites, err := cleanup(ctx, db, cfg.prefix)
	if err != nil {
		return err
	}
	// Vacuum the deleted rows' space so the new seed reuses it: otherwise a
	// reseeded articles table is twice the size, and scans of it measure
	// slower than a fresh seed's.
	if users > 0 || sites > 0 {
		if _, err := db.ExecContext(ctx, `VACUUM `+seedTables); err != nil {
			return fmt.Errorf("vacuum: %w", err)
		}
	}
	fmt.Fprintf(out, "Cleaned up %d users and %d sites in %s\n", users, sites, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	rng := rand.New(rand.NewPCG(uint64(cfg.seed), 0))
	feeds := planFeeds(cfg, rng)
	if err := seedFeeds(ctx, db, cfg, feeds); err != nil {
		return err
	}
	// The stars query below plans against these statistics; after the
	// cleanup's VACUUM they'd say articles is nearly empty.
	if _, err := db.ExecContext(ctx, `ANALYZE sites, feeds, articles`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	fmt.Fprintf(out, "Seeded feeds and articles in %s\n", time.Since(start).Round(time.Millisecond))

	stepStart := time.Now()
	if err := seedUsers(ctx, db, cfg, feeds, rng); err != nil {
		return err
	}
	fmt.Fprintf(out, "Seeded users, subscriptions, tags, stars and timelines in %s\n", time.Since(stepStart).Round(time.Millisecond))

	stepStart = time.Now()
	if _, err := db.ExecContext(ctx, `ANALYZE `+seedTables); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	fmt.Fprintf(out, "Analyzed in %s\n", time.Since(stepStart).Round(time.Millisecond))
	fmt.Fprintf(out, "Seeding took %s\n\n", time.Since(start).Round(time.Millisecond))

	return printCounts(ctx, db, cfg.prefix, out)
}

// seedFeeds inserts feeds' sites, feeds and articles in one transaction,
// and sets each feed's id.
func seedFeeds(ctx context.Context, db *sql.DB, cfg seedConfig, feeds []feedPlan) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	if err := setSeed(ctx, tx, cfg.seed); err != nil {
		return err
	}

	hosts := make([]string, len(feeds))
	perWeek := make([]float64, len(feeds))
	counts := make([]int64, len(feeds))
	for i, f := range feeds {
		hosts[i], perWeek[i], counts[i] = f.host, f.perDay*7, int64(f.articles)
	}

	// Sites and feeds are fresh and not due for 100 years, so a dev server running
	// against the seed doesn't fetch their example.com URLs.
	stmt := `INSERT INTO sites (host, url, title, icon_checked_at, icon_next_check_at)
		SELECT host, 'https://' || host || '/', 'Site ' || n, now(), now() + interval '100 years'
		FROM unnest($1::text[]) WITH ORDINALITY AS u (host, n)`
	if _, err := tx.ExecContext(ctx, stmt, pq.Array(hosts)); err != nil {
		return fmt.Errorf("insert sites: %w", err)
	}

	stmt = `INSERT INTO feeds (site_id, url, site_url, title, posts_per_week, last_fetched_at, last_attempt_at, next_fetch_at)
		SELECT s.id, s.url || 'feed.xml', s.url, 'Feed ' || u.n, u.per_week, now(), now(), now() + interval '100 years'
		FROM unnest($1::text[], $2::real[]) WITH ORDINALITY AS u (host, per_week, n)
		JOIN sites s ON s.host = u.host`
	if _, err := tx.ExecContext(ctx, stmt, pq.Array(hosts), pq.Array(perWeek)); err != nil {
		return fmt.Errorf("insert feeds: %w", err)
	}

	stmt = `SELECT f.id
		FROM unnest($1::text[]) WITH ORDINALITY AS u (host, n)
		JOIN sites s ON s.host = u.host
		JOIN feeds f ON f.site_id = s.id
		ORDER BY u.n`
	rows, err := tx.QueryContext(ctx, stmt, pq.Array(hosts))
	if err != nil {
		return fmt.Errorf("read feed IDs: %w", err)
	}
	ids := make([]string, 0, len(feeds))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) != len(feeds) {
		return fmt.Errorf("inserted %d feeds, want %d", len(ids), len(feeds))
	}
	for i := range feeds {
		feeds[i].id = ids[i]
	}

	// Each article's published date is uniform over the period, and
	// timeline_at is the published date, as for a feed's backlog; created_at
	// is up to an hour after that (capped at now), so the grouped timeline's
	// news has arrivals to find. About
	// copyShare of articles also get a copy in another feed (the LATERAL
	// adds it): the same canonical URL, a tracking parameter on its link, and
	// a position up to 6 hours later.
	//
	// The scalar subqueries refer to i so they run per article, and random()
	// in the derived table's select list keeps it from being flattened, so
	// each article draws its values once, in a fixed order: the same -seed
	// seeds the same articles.
	stmt = `INSERT INTO articles (feed_id, external_id, url, canonical_url, media_type, title, summary, excerpt, published_at, timeline_at, created_at)
		SELECT
			CASE WHEN c.copy THEN ($1::uuid[])[s.target] ELSE s.feed_id END,
			CASE WHEN c.copy THEN 'copy-' || s.k || '-' || s.i ELSE 'item-' || s.i END,
			CASE WHEN c.copy THEN s.link || '?utm_source=feed' ELSE s.link END,
			s.link, s.media_type, s.title,
			'<p>' || s.excerpt || '</p><p>' || s.excerpt || '</p>', s.excerpt,
			CASE WHEN c.copy THEN LEAST(now(), s.p + s.lag) ELSE s.p END,
			CASE WHEN c.copy THEN LEAST(now(), s.p + s.lag) ELSE s.p END,
			LEAST(now(), CASE WHEN c.copy THEN LEAST(now(), s.p + s.lag) ELSE s.p END + s.arrival)
		FROM (
			SELECT f.id AS feed_id, f.k, i,
				'https://' || f.host || '/posts/' || i AS link,
				CASE WHEN random() < $6 THEN 'audio/mpeg' ELSE '' END AS media_type,
				initcap((SELECT string_agg(($4::text[])[1 + floor(random() * cardinality($4::text[]))::int], ' ')
					FROM generate_series(1, 3 + i % 5)))
					|| CASE WHEN random() < $7 THEN ' ' || ($5::text[])[1 + floor(random() * cardinality($5::text[]))::int] ELSE '' END AS title,
				(SELECT string_agg(($4::text[])[1 + floor(random() * cardinality($4::text[]))::int], ' ')
					FROM generate_series(1, 18 + i % 7)) AS excerpt,
				now() - random() * $8 * interval '1 day' AS p,
				$10 > 1 AND random() < $9 AS copied,
				-- Any feed but the article's own (k is 1-based).
				(1 + (f.k + floor(random() * ($10::int - 1))::int) % $10::int)::int AS target,
				random() * interval '6 hours' AS lag,
				random() * interval '1 hour' AS arrival
			FROM unnest($1::uuid[], $2::text[], $3::int[]) WITH ORDINALITY AS f (id, host, n, k),
				generate_series(1, f.n) AS i
		) s
		CROSS JOIN LATERAL (SELECT false AS copy UNION ALL SELECT true WHERE s.copied) c`
	_, err = tx.ExecContext(ctx, stmt, pq.Array(ids), pq.Array(hosts), pq.Array(counts),
		pq.Array(commonWords), pq.Array(rareWords), mediaShare, rareWordShare, cfg.years*365, copyShare, len(ids))
	if err != nil {
		return fmt.Errorf("insert articles: %w", err)
	}

	// The seed bypasses saveArticles, so flag both copies of each pair as it
	// would.
	stmt = `UPDATE articles a SET has_copies = true
		WHERE a.feed_id = ANY($1) AND a.canonical_url IN (
			SELECT canonical_url FROM articles WHERE feed_id = ANY($1) AND external_id LIKE 'copy-%')`
	if _, err := tx.ExecContext(ctx, stmt, pq.Array(ids)); err != nil {
		return fmt.Errorf("flag copies: %w", err)
	}

	return tx.Commit()
}

// setSeed seeds the session's random() from seed.
func setSeed(ctx context.Context, tx *sql.Tx, seed int64) error {
	// setseed takes a number in [-1, 1].
	_, err := tx.ExecContext(ctx, `SELECT setseed($1)`, math.Mod(float64(seed), 1e6)/1e6)
	return err
}

// seedUsers creates the main and baseline users, their subscriptions, the
// main user's tags and stars, and its custom timelines.
func seedUsers(ctx context.Context, db *sql.DB, cfg seedConfig, feeds []feedPlan, rng *rand.Rand) error {
	usersRepo := psql.NewUserRepository(db)
	mainID, err := createUser(ctx, db, usersRepo, "Load seed", mainEmail(cfg.prefix))
	if err != nil {
		return err
	}
	smallID, err := createUser(ctx, db, usersRepo, "Load seed (small)", smallEmail(cfg.prefix))
	if err != nil {
		return err
	}

	mainFeeds := pickFeeds(feeds, cfg.subscriptions, mainBusySubscriptions, rng)
	smallFeeds := pickFeeds(feeds, min(smallSubscriptions, len(feeds)), 1, rng)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	if err := subscribe(ctx, tx, mainID, mainFeeds); err != nil {
		return err
	}
	if err := subscribe(ctx, tx, smallID, smallFeeds); err != nil {
		return err
	}
	tagOnly, err := seedTags(ctx, tx, mainID, mainFeeds, rng)
	if err != nil {
		return err
	}
	if err := seedStars(ctx, tx, mainID, cfg.seed); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return seedTimelines(ctx, db, mainID, tagOnly)
}

// createUser creates a user with seedPassword and a confirmed email.
func createUser(ctx context.Context, db *sql.DB, users *psql.UserRepository, name, email string) (string, error) {
	id, _, err := users.Create(ctx, name, email, seedPassword)
	if err != nil {
		return "", fmt.Errorf("create user %s: %w", email, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET email_confirmed_at = now() WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("confirm user %s: %w", email, err)
	}
	return id, nil
}

// pickFeeds returns n of feeds: up to nBusy busy ones, then others chosen
// at random (busy ones only once the others run out).
func pickFeeds(feeds []feedPlan, n, nBusy int, rng *rand.Rand) []feedPlan {
	var busyFeeds, others []feedPlan
	for _, f := range feeds {
		if f.kind == busy {
			busyFeeds = append(busyFeeds, f)
		} else {
			others = append(others, f)
		}
	}
	nBusy = min(nBusy, len(busyFeeds), n)
	rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })

	picked := append([]feedPlan{}, busyFeeds[:nBusy]...)
	picked = append(picked, others[:min(len(others), n-nBusy)]...)
	return append(picked, busyFeeds[nBusy:nBusy+(n-len(picked))]...)
}

func subscribe(ctx context.Context, tx *sql.Tx, userID string, feeds []feedPlan) error {
	stmt := `INSERT INTO subscriptions (user_id, feed_id) SELECT $1, unnest($2::uuid[])`
	if _, err := tx.ExecContext(ctx, stmt, userID, pq.Array(feedIDs(feeds))); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	return nil
}

// seedTags gives userID tagCount tags: the first on up to 5 of its weekly
// feeds, the others on 3–30 of its feeds each. It returns the first's ID.
func seedTags(ctx context.Context, tx *sql.Tx, userID string, subscribed []feedPlan, rng *rand.Rand) (string, error) {
	var tagOnly string
	for n := 1; n <= tagCount; n++ {
		var feeds []feedPlan
		if n == 1 {
			for _, f := range subscribed {
				if f.kind == weekly && len(feeds) < 5 {
					feeds = append(feeds, f)
				}
			}
		} else {
			feeds = append([]feedPlan{}, subscribed...)
			rng.Shuffle(len(feeds), func(i, j int) { feeds[i], feeds[j] = feeds[j], feeds[i] })
			feeds = feeds[:min(len(feeds), 3+rng.IntN(28))]
		}

		var id string
		err := tx.QueryRowContext(ctx, `INSERT INTO tags (user_id, name) VALUES ($1, $2) RETURNING id`, userID, fmt.Sprintf("Tag %d", n)).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("insert tag: %w", err)
		}
		stmt := `INSERT INTO subscription_tags (user_id, feed_id, tag_id) SELECT $1, unnest($2::uuid[]), $3`
		if _, err := tx.ExecContext(ctx, stmt, userID, pq.Array(feedIDs(feeds)), id); err != nil {
			return "", fmt.Errorf("tag feeds: %w", err)
		}
		if n == 1 {
			tagOnly = id
		}
	}
	return tagOnly, nil
}

// seedStars stars starCount of the articles userID subscribes to, at most
// one copy of each, each starred within two days of its timeline position.
// They're chosen by a hash of the feed's URL, the article's external ID and
// seed rather than by random(), which would follow the order rows are
// scanned in.
func seedStars(ctx context.Context, tx *sql.Tx, userID string, seed int64) error {
	stmt := `INSERT INTO stars (user_id, article_id, starred_at)
		SELECT $1, id, LEAST(now(), timeline_at + (h & 65535) / 65536.0 * interval '2 days')
		FROM (
			SELECT DISTINCT ON (dedup_key) id, timeline_at, h
			FROM (
				SELECT a.id, a.timeline_at, hashtext(f.url || ' ' || a.external_id || ' ' || $3) AS h,
					CASE WHEN a.canonical_url = '' THEN a.id::text ELSE a.canonical_url END AS dedup_key
				FROM subscriptions sub
				JOIN articles a ON a.feed_id = sub.feed_id
				JOIN feeds f ON f.id = a.feed_id
				WHERE sub.user_id = $1
				ORDER BY h, f.url, a.external_id
				LIMIT $2 * 2
			) sample
			ORDER BY dedup_key, h
		) one_copy
		ORDER BY h
		LIMIT $2`
	if _, err := tx.ExecContext(ctx, stmt, userID, starCount, strconv.FormatInt(seed, 10)); err != nil {
		return fmt.Errorf("insert stars: %w", err)
	}
	return nil
}

// seedTimelines creates userID's custom timelines and shares the Words one.
func seedTimelines(ctx context.Context, db *sql.DB, userID, tagOnly string) error {
	repo := psql.NewTimelineRepository(db)
	timelines := []rss.Timeline{
		{Name: timelineTagOnly, Filter: rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{tagOnly}}},
		{Name: timelineWords, Filter: rss.TimelineFilter{IncludeWords: rareWords}},
		{Name: timelineQuiet, Filter: rss.TimelineFilter{MaxPostsPerWeek: 3}},
		{Name: timelineMedia, Filter: rss.TimelineFilter{MediaOnly: true}},
		{Name: timelineStarred, Filter: rss.TimelineFilter{StarredOnly: true}},
	}
	for _, t := range timelines {
		id, err := repo.Create(ctx, userID, t)
		if err != nil {
			return fmt.Errorf("create timeline %s: %w", t.Name, err)
		}
		if t.Name != timelineWords {
			continue
		}
		tok, err := token.Random()
		if err != nil {
			return err
		}
		if _, err := repo.Share(ctx, userID, id, tok); err != nil {
			return fmt.Errorf("share timeline %s: %w", t.Name, err)
		}
	}
	return nil
}

func feedIDs(feeds []feedPlan) []string {
	ids := make([]string, len(feeds))
	for i, f := range feeds {
		ids[i] = f.id
	}
	return ids
}

// printCounts prints the row counts of prefix's seed.
func printCounts(ctx context.Context, db *sql.DB, prefix string, out io.Writer) error {
	counts := []struct {
		label string
		stmt  string
	}{
		{"sites", `SELECT count(*) FROM sites WHERE host ~ $1`},
		{"feeds", `SELECT count(*) FROM feeds f JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1`},
		{"articles", `SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1`},
		{"  of them copies", `SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1 AND a.external_id LIKE 'copy-%'`},
		{"  with media", `SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1 AND a.media_type <> ''`},
	}
	userCounts := []struct {
		label string
		stmt  string
	}{
		{"subscriptions", `SELECT count(*) FROM subscriptions sub JOIN users u ON u.id = sub.user_id WHERE u.email = $1`},
		{"subscribed articles", `SELECT count(*) FROM subscriptions sub JOIN users u ON u.id = sub.user_id JOIN articles a ON a.feed_id = sub.feed_id WHERE u.email = $1`},
		{"tags", `SELECT count(*) FROM tags t JOIN users u ON u.id = t.user_id WHERE u.email = $1`},
		{"tagged subscriptions", `SELECT count(*) FROM subscription_tags st JOIN users u ON u.id = st.user_id WHERE u.email = $1`},
		{"stars", `SELECT count(*) FROM stars s JOIN users u ON u.id = s.user_id WHERE u.email = $1`},
		{"timelines", `SELECT count(*) FROM timelines t JOIN users u ON u.id = t.user_id WHERE u.email = $1`},
		{"shared timelines", `SELECT count(*) FROM timelines t JOIN users u ON u.id = t.user_id WHERE u.email = $1 AND t.share_token IS NOT NULL`},
	}

	fmt.Fprintln(out, "| Rows | Count |")
	fmt.Fprintln(out, "| --- | ---: |")
	for _, c := range counts {
		var n int64
		if err := db.QueryRowContext(ctx, c.stmt, hostPattern(prefix)).Scan(&n); err != nil {
			return fmt.Errorf("count %s: %w", c.label, err)
		}
		fmt.Fprintf(out, "| %s | %s |\n", c.label, thousands(n))
	}
	for _, email := range []string{mainEmail(prefix), smallEmail(prefix)} {
		for _, c := range userCounts {
			var n int64
			if err := db.QueryRowContext(ctx, c.stmt, email).Scan(&n); err != nil {
				return fmt.Errorf("count %s: %w", c.label, err)
			}
			if n == 0 && email == smallEmail(prefix) && c.label != "subscriptions" && c.label != "subscribed articles" {
				continue
			}
			fmt.Fprintf(out, "| %s: %s | %s |\n", email, c.label, thousands(n))
		}
	}
	fmt.Fprintf(out, "\nLog in as %s or %s with the password %q.\n", mainEmail(prefix), smallEmail(prefix), seedPassword)
	return nil
}

// thousands formats n with commas between groups of three digits.
func thousands(n int64) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
