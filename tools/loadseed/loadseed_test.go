package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
)

// seedCounts are the rows a seed made, to compare two seeds.
type seedCounts struct {
	sites, feeds, articles                    int
	mainSubscriptions, smallSubscriptions     int
	tags, taggedSubscriptions, stars          int
	timelines, sharedTimelines, emptyFeeds    int
	copiedURLs, feedsWithMoreArticlesThanBusy int
}

func countSeed(t *testing.T, db *sql.DB, prefix string) seedCounts {
	t.Helper()
	ctx := context.Background()
	host := hostPattern(prefix)
	var c seedCounts
	queries := []struct {
		dest *int
		stmt string
		arg  string
	}{
		{&c.sites, `SELECT count(*) FROM sites WHERE host ~ $1`, host},
		{&c.feeds, `SELECT count(*) FROM feeds f JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1`, host},
		{&c.articles, `SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1`, host},
		{&c.emptyFeeds, `SELECT count(*) FROM feeds f JOIN sites s ON s.id = f.site_id
			WHERE s.host ~ $1 AND NOT EXISTS (SELECT 1 FROM articles a WHERE a.feed_id = f.id)`, host},
		// Weekly feeds (1 a week) with at least as many articles as the
		// busy feed with the fewest (20 a day or more).
		{&c.feedsWithMoreArticlesThanBusy, `WITH n AS (
				SELECT f.posts_per_week, (SELECT count(*) FROM articles a WHERE a.feed_id = f.id) AS articles
				FROM feeds f JOIN sites s ON s.id = f.site_id WHERE s.host ~ $1
			)
			SELECT count(*) FROM n
			WHERE posts_per_week < 2 AND articles >= (SELECT min(articles) FROM n WHERE posts_per_week >= 140)`, host},
		{&c.copiedURLs, `SELECT count(DISTINCT a.canonical_url)
			FROM articles a
			JOIN feeds f ON f.id = a.feed_id
			JOIN sites s ON s.id = f.site_id
			JOIN articles b ON b.canonical_url = a.canonical_url AND b.feed_id <> a.feed_id
			WHERE s.host ~ $1 AND a.canonical_url <> ''`, host},
		{&c.mainSubscriptions, `SELECT count(*) FROM subscriptions sub JOIN users u ON u.id = sub.user_id WHERE u.email = $1`, mainEmail(prefix)},
		{&c.smallSubscriptions, `SELECT count(*) FROM subscriptions sub JOIN users u ON u.id = sub.user_id WHERE u.email = $1`, smallEmail(prefix)},
		{&c.tags, `SELECT count(*) FROM tags t JOIN users u ON u.id = t.user_id WHERE u.email = $1`, mainEmail(prefix)},
		{&c.taggedSubscriptions, `SELECT count(*) FROM subscription_tags st JOIN users u ON u.id = st.user_id WHERE u.email = $1`, mainEmail(prefix)},
		{&c.stars, `SELECT count(*) FROM stars s JOIN users u ON u.id = s.user_id WHERE u.email = $1`, mainEmail(prefix)},
		{&c.timelines, `SELECT count(*) FROM timelines t JOIN users u ON u.id = t.user_id WHERE u.email = $1`, mainEmail(prefix)},
		{&c.sharedTimelines, `SELECT count(*) FROM timelines t JOIN users u ON u.id = t.user_id WHERE u.email = $1 AND t.share_token IS NOT NULL`, mainEmail(prefix)},
	}
	for _, q := range queries {
		if err := db.QueryRowContext(ctx, q.stmt, q.arg).Scan(q.dest); err != nil {
			t.Fatalf("%s: %v", q.stmt, err)
		}
	}
	return c
}

func TestSeedSmall(t *testing.T) {
	db := psqltest.NewDB(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("loadseed-test-%x", rand.Uint64())
	cfg := seedConfig{prefix: prefix, feeds: 20, subscriptions: 10, years: 30.0 / 365, seed: 1}

	t.Cleanup(func() {
		if _, _, err := cleanup(context.Background(), db, prefix); err != nil {
			t.Errorf("cleanup: %v", err)
		}
		if got := countSeed(t, db, prefix); got != (seedCounts{}) {
			t.Errorf("after cleanup: %+v, want no rows", got)
		}
	})

	if err := seed(ctx, db, cfg, io.Discard); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := countSeed(t, db, prefix)

	if _, err := psql.NewUserRepository(db).GetByEmail(ctx, mainEmail(prefix)); err != nil {
		t.Errorf("GetByEmail(%s): %v", mainEmail(prefix), err)
	}
	if first.sites != 20 || first.feeds != 20 {
		t.Errorf("sites, feeds = %d, %d; want 20, 20", first.sites, first.feeds)
	}
	if first.mainSubscriptions != 10 {
		t.Errorf("main user's subscriptions = %d; want 10", first.mainSubscriptions)
	}
	if first.smallSubscriptions != 20 {
		t.Errorf("small user's subscriptions = %d; want 20 (all the feeds)", first.smallSubscriptions)
	}
	if first.timelines != 5 || first.sharedTimelines != 1 {
		t.Errorf("timelines, shared = %d, %d; want 5, 1", first.timelines, first.sharedTimelines)
	}
	if first.tags != tagCount || first.taggedSubscriptions == 0 {
		t.Errorf("tags, tagged subscriptions = %d, %d; want %d, > 0", first.tags, first.taggedSubscriptions, tagCount)
	}
	if first.stars == 0 {
		t.Error("no stars")
	}
	if first.emptyFeeds != 0 {
		t.Errorf("%d feeds have no articles", first.emptyFeeds)
	}
	if first.feedsWithMoreArticlesThanBusy != 0 {
		t.Errorf("%d weekly feeds have as many articles as a busy one", first.feedsWithMoreArticlesThanBusy)
	}
	if first.copiedURLs == 0 {
		t.Error("no canonical_url is shared across feeds")
	}

	// Seeding again cleans up the first seed.
	if err := seed(ctx, db, cfg, io.Discard); err != nil {
		t.Fatalf("seed again: %v", err)
	}
	if again := countSeed(t, db, prefix); again != first {
		t.Errorf("after seeding again: %+v; want %+v", again, first)
	}

	// The report runs against the small seed.
	var out strings.Builder
	if err := measure(ctx, psqltest.DSN(t), prefix, &out); err != nil {
		t.Fatalf("measure: %v", err)
	}
	for _, want := range []string{"| ListTimeline: Home, main user, page 1 |", "| ListShared: Words |", "| ListStarred: Starred, page 1 |"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRefusesNonDevDatabase(t *testing.T) {
	// The host can't resolve, so a connection attempt would fail
	// differently.
	for _, dsn := range []string{
		"postgres://rssapp:pw@host.invalid:5432/rss?sslmode=disable",
		"host=host.invalid dbname=rss user=rssapp sslmode=disable",
		"postgres://rssapp:pw@host.invalid:5432/rss_dev_backup?sslmode=disable",
	} {
		getenv := func(string) string { return "" }
		err := run(context.Background(), []string{"-db-dsn", dsn}, getenv, io.Discard)
		if !errors.Is(err, errNotDevDatabase) {
			t.Errorf("run with %q: %v; want errNotDevDatabase", dsn, err)
		}
	}
}

func TestCheckDevDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://rssapp:pw@localhost:5432/rssapp_dev?sslmode=disable",
		"postgres://rssapp:pw@localhost:5432/rssapp_test?sslmode=disable",
		"host=localhost dbname=rssapp_test user=rssapp",
	} {
		if err := checkDevDatabase(dsn); err != nil {
			t.Errorf("checkDevDatabase(%q): %v; want nil", dsn, err)
		}
	}
}

func TestStatsOf(t *testing.T) {
	plan := `Limit  (cost=1.00..2.00 rows=31 width=100) (actual time=10.000..10.100 rows=31 loops=1)
  Buffers: shared hit=1200 read=34 written=2, temp read=56 written=56
  ->  Sort  (cost=1.00..2.00 rows=100 width=100) (actual time=10.000..10.050 rows=31 loops=1)
        Buffers: shared hit=1200 read=34
        ->  Nested Loop  (cost=0.00..1.00 rows=100 width=100) (actual time=0.010..9.000 rows=5000 loops=1)
              ->  Index Only Scan using subscriptions_pkey on subscriptions sub  (cost=0.00..1.00 rows=10 width=16) (actual time=0.005..0.010 rows=10 loops=1)
              ->  Index Scan using articles_feed_timeline_idx on articles a  (cost=0.00..1.00 rows=500 width=100) (actual time=0.005..0.500 rows=480 loops=10)
                    Filter: (media_type <> ''::text)
                    Rows Removed by Filter: 20
        ->  CTE Scan on mine  (cost=0.00..1.00 rows=10 width=16) (actual time=0.005..0.010 rows=7 loops=1)`
	got := statsOf(plan)
	want := planStats{buffersHit: 1200, buffersRead: 34, tempRead: 56, rowsRead: 10 + 480*10 + 20*10}
	if got != want {
		t.Errorf("statsOf = %+v; want %+v", got, want)
	}
}
