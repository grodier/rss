package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// The limits the handlers pass: a page of 30 plus one to know there's a
// next page, and the shared feed's 50.
const (
	pageLimit   = 31
	pageSize    = 30
	sharedLimit = 50
	deepPage    = 20
	runs        = 5
)

// measurement is one repository call to time.
type measurement struct {
	name string
	call func(ctx context.Context) (rows int, err error)
}

// result is what measuring one call found.
type result struct {
	name    string
	rows    int
	median  time.Duration
	slowest time.Duration
	plans   []plan
}

// plan is one statement's auto_explain output.
type plan struct {
	duration string // as auto_explain reports it, e.g. "12.345 ms"
	text     string // the plan, without the query text
}

// planCollector gathers auto_explain's notices.
type planCollector struct {
	mu    sync.Mutex
	plans []plan
}

func (c *planCollector) notice(e *pq.Error) {
	duration, text, ok := parsePlanNotice(e.Message)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans = append(c.plans, plan{duration: duration, text: text})
}

func (c *planCollector) take() []plan {
	c.mu.Lock()
	defer c.mu.Unlock()
	plans := c.plans
	c.plans = nil
	return plans
}

var planNotice = regexp.MustCompile(`^duration: ([0-9.]+ ms)\s+plan:\n`)

// parsePlanNotice splits an auto_explain notice into the statement's
// duration and its plan, dropping the query text before the first plan node
// (the first line with "(cost="). ok is false for any other notice.
func parsePlanNotice(msg string) (duration, text string, ok bool) {
	m := planNotice.FindStringSubmatch(msg)
	if m == nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(msg[len(m[0]):], "\n"), "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "(cost=") })
	if i < 0 {
		return "", "", false
	}
	return m[1], strings.Join(lines[i:], "\n"), true
}

// measure times the reading queries of prefix's seed, on one connection so
// auto_explain's settings apply to every call, and prints a report to out.
// The connection is set up as the app's are (psql.NewConnector).
func measure(ctx context.Context, dsn, prefix string, out io.Writer) error {
	connector, err := psql.NewConnector(dsn)
	if err != nil {
		return err
	}
	var collector planCollector
	db := sql.OpenDB(pq.ConnectorWithNoticeHandler(connector, collector.notice))
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := printServer(ctx, db, out); err != nil {
		return err
	}
	explain := true
	if _, err := db.ExecContext(ctx, `LOAD 'auto_explain'`); err != nil {
		explain = false
		fmt.Fprintf(out, "auto_explain can't be loaded (%v), so this report has timings only: no plans, buffers or rows read.\n\n", err)
	} else {
		for _, stmt := range []string{
			`SET auto_explain.log_min_duration = -1`,
			`SET auto_explain.log_analyze = on`,
			`SET auto_explain.log_buffers = on`,
			`SET auto_explain.log_level = notice`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
	}
	// plans turns auto_explain's logging of every statement on or off.
	plans := func(on bool) error {
		if !explain {
			return nil
		}
		d := "-1"
		if on {
			d = "0"
		}
		_, err := db.ExecContext(ctx, `SET auto_explain.log_min_duration = `+d)
		return err
	}

	ms, err := measurements(ctx, db, prefix)
	if err != nil {
		return err
	}

	var results []result
	for _, m := range ms {
		// One warm-up call, one with plans, then the timed calls without
		// auto_explain's instrumentation.
		if _, err := m.call(ctx); err != nil {
			return fmt.Errorf("%s: %w", m.name, err)
		}
		if err := plans(true); err != nil {
			return err
		}
		collector.take()
		_, err := m.call(ctx)
		if perr := plans(false); err == nil {
			err = perr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", m.name, err)
		}
		r := result{name: m.name, plans: collector.take()}

		durations := make([]time.Duration, runs)
		for i := range durations {
			start := time.Now()
			rows, err := m.call(ctx)
			durations[i] = time.Since(start)
			if err != nil {
				return fmt.Errorf("%s: %w", m.name, err)
			}
			r.rows = rows
		}
		slices.Sort(durations)
		r.median, r.slowest = durations[len(durations)/2], durations[len(durations)-1]
		results = append(results, r)
	}

	printReport(out, results, explain)
	return nil
}

// printServer prints the database server's version and memory settings.
func printServer(ctx context.Context, db *sql.DB, out io.Writer) error {
	var version, sharedBuffers, workMem string
	err := db.QueryRowContext(ctx, `SELECT version(), current_setting('shared_buffers'), current_setting('work_mem')`).Scan(&version, &sharedBuffers, &workMem)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s\nshared_buffers = %s, work_mem = %s\n\n", version, sharedBuffers, workMem)
	return nil
}

// measurements returns the calls to time for prefix's seed, with their
// arguments looked up the way the handlers look them up.
func measurements(ctx context.Context, db *sql.DB, prefix string) ([]measurement, error) {
	users := psql.NewUserRepository(db)
	timelineRepo := psql.NewTimelineRepository(db)
	articles := psql.NewArticleRepository(db)
	stars := psql.NewStarRepository(db)

	var ms []measurement
	timeline := func(name, userID string, filter rss.TimelineFilter, before rss.ArticleCursor) {
		ms = append(ms, measurement{name: name, call: func(ctx context.Context) (int, error) {
			items, err := articles.ListTimeline(ctx, userID, filter, before, pageLimit)
			return len(items), err
		}})
	}
	// home adds Home's page 1 and deep page for userID.
	home := func(label, userID string) error {
		timeline(fmt.Sprintf("ListTimeline: Home, %s, page 1", label), userID, rss.TimelineFilter{}, rss.ArticleCursor{})
		page, before, err := followTimeline(ctx, articles, userID, deepPage)
		if err != nil {
			return err
		}
		timeline(fmt.Sprintf("ListTimeline: Home, %s, page %d", label, page), userID, rss.TimelineFilter{}, before)
		return nil
	}

	mainUser, err := users.GetByEmail(ctx, mainEmail(prefix))
	if err != nil {
		return nil, fmt.Errorf("user %s (seed first): %w", mainEmail(prefix), err)
	}
	small, err := users.GetByEmail(ctx, smallEmail(prefix))
	if err != nil {
		return nil, fmt.Errorf("user %s (seed first): %w", smallEmail(prefix), err)
	}
	if err := home("main user", mainUser.ID); err != nil {
		return nil, err
	}
	if err := home("small user", small.ID); err != nil {
		return nil, err
	}

	list, err := timelineRepo.List(ctx, mainUser.ID)
	if err != nil {
		return nil, err
	}
	starred := func(name string, filter rss.TimelineFilter) {
		ms = append(ms, measurement{name: name, call: func(ctx context.Context) (int, error) {
			items, err := stars.ListStarred(ctx, mainUser.ID, filter, rss.ArticleCursor{}, pageLimit)
			return len(items), err
		}})
	}
	var shared []rss.Timeline
	filters := map[string]rss.TimelineFilter{}
	for _, t := range list {
		t, err := timelineRepo.Get(ctx, mainUser.ID, t.ID)
		if err != nil {
			return nil, err
		}
		filters[t.Name] = t.Filter
		if t.Filter.StarredOnly {
			starred("ListStarred: "+t.Name+", page 1", t.Filter)
		} else {
			timeline("ListTimeline: "+t.Name+", page 1", mainUser.ID, t.Filter, rss.ArticleCursor{})
		}
		if t.ShareToken != "" {
			shared = append(shared, t)
		}
	}
	// Starred with Show limited to a tag, which ListStarred can't serve from
	// the stars index alone: Starred's filter with Tag only's sources.
	if f, ok := filters[timelineStarred]; ok {
		if tag, ok := filters[timelineTagOnly]; ok {
			f.OnlyIncluded, f.IncludeTagIDs = true, tag.IncludeTagIDs
			starred("ListStarred: Starred, Show limited to Tag only's tag, page 1", f)
		}
	}
	for _, t := range shared {
		st, ownerID, err := timelineRepo.GetShared(ctx, t.ShareToken)
		if err != nil {
			return nil, err
		}
		ms = append(ms, measurement{name: "ListShared: " + st.Name, call: func(ctx context.Context) (int, error) {
			items, err := articles.ListShared(ctx, ownerID, st.Filter, sharedLimit)
			return len(items), err
		}})
	}

	var feedID string
	stmt := `SELECT f.id FROM feeds f JOIN sites s ON s.id = f.site_id
		WHERE s.host ~ $1
		ORDER BY f.posts_per_week DESC, f.url
		LIMIT 1`
	if err := db.QueryRowContext(ctx, stmt, hostPattern(prefix)).Scan(&feedID); err != nil {
		return nil, fmt.Errorf("busiest feed: %w", err)
	}
	byFeed := func(name string, before rss.ArticleCursor) {
		ms = append(ms, measurement{name: name, call: func(ctx context.Context) (int, error) {
			items, err := articles.ListByFeed(ctx, feedID, before, pageLimit)
			return len(items), err
		}})
	}
	byFeed("ListByFeed: busiest feed, page 1", rss.ArticleCursor{})
	page, before, err := followFeed(ctx, articles, feedID, deepPage)
	if err != nil {
		return nil, err
	}
	byFeed(fmt.Sprintf("ListByFeed: busiest feed, page %d", page), before)

	return ms, nil
}

// followTimeline pages through userID's Home as the timeline handler does,
// and returns the page number it reached (up to page) and that page's
// cursor.
func followTimeline(ctx context.Context, articles *psql.ArticleRepository, userID string, page int) (int, rss.ArticleCursor, error) {
	var before rss.ArticleCursor
	n := 1
	for ; n < page; n++ {
		items, err := articles.ListTimeline(ctx, userID, rss.TimelineFilter{}, before, pageLimit)
		if err != nil {
			return 0, rss.ArticleCursor{}, err
		}
		if len(items) <= pageSize {
			break
		}
		last := items[pageSize-1].Article
		before = rss.ArticleCursor{At: last.TimelineAt, ID: last.ID}
	}
	return n, before, nil
}

// followFeed pages through feedID's articles as the feed handler does, like
// followTimeline.
func followFeed(ctx context.Context, articles *psql.ArticleRepository, feedID string, page int) (int, rss.ArticleCursor, error) {
	var before rss.ArticleCursor
	n := 1
	for ; n < page; n++ {
		items, err := articles.ListByFeed(ctx, feedID, before, pageLimit)
		if err != nil {
			return 0, rss.ArticleCursor{}, err
		}
		if len(items) <= pageSize {
			break
		}
		last := items[pageSize-1]
		before = rss.ArticleCursor{At: last.FeedSortAt(), ID: last.ID}
	}
	return n, before, nil
}

// planStats are the totals of a plan that the report shows.
type planStats struct {
	buffersHit, buffersRead int64 // the top node's shared buffers, which include its children's
	tempRead                int64 // the top node's temporary blocks read: sorts and hashes spilled to disk
	rowsRead                int64 // rows the table and index scans produced or filtered out
}

var (
	nodeLine     = regexp.MustCompile(`^\s*(?:->\s+)?(?:Parallel\s+)?([A-Za-z ]+?)(?: using | on |\s+\()`)
	actualRows   = regexp.MustCompile(`actual time=[0-9.]+\.\.[0-9.]+ rows=([0-9]+) loops=([0-9]+)`)
	removedRows  = regexp.MustCompile(`^\s*Rows Removed by (?:Filter|Index Recheck|Join Filter): ([0-9]+)`)
	buffersLine  = regexp.MustCompile(`^\s*Buffers: (.*)`)
	bufferCounts = regexp.MustCompile(`(hit|read)=([0-9]+)`)
)

// relationScans are the plan nodes that read a table or index.
var relationScans = map[string]bool{
	"Seq Scan": true, "Index Scan": true, "Index Only Scan": true, "Bitmap Heap Scan": true,
}

// statsOf totals a plan's buffers and the rows its relation scans read.
func statsOf(text string) planStats {
	var s planStats
	var scanLoops int64 // loops of the current node if it's a relation scan, else 0
	topBuffers := false
	for _, line := range strings.Split(text, "\n") {
		if m := actualRows.FindStringSubmatch(line); m != nil {
			scanLoops = 0
			if n := nodeLine.FindStringSubmatch(line); n != nil && relationScans[n[1]] {
				rows, _ := strconv.ParseInt(m[1], 10, 64)
				loops, _ := strconv.ParseInt(m[2], 10, 64)
				s.rowsRead += rows * loops
				scanLoops = loops
			}
			continue
		}
		if m := removedRows.FindStringSubmatch(line); m != nil && scanLoops > 0 {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			s.rowsRead += n * scanLoops
			continue
		}
		if m := buffersLine.FindStringSubmatch(line); m != nil && !topBuffers {
			// shared hit=1 read=2 written=3, temp read=4 written=5
			topBuffers = true
			for _, part := range strings.Split(m[1], ", ") {
				kind, counts, _ := strings.Cut(part, " ")
				for _, c := range bufferCounts.FindAllStringSubmatch(counts, -1) {
					n, _ := strconv.ParseInt(c[2], 10, 64)
					switch {
					case kind == "shared" && c[1] == "hit":
						s.buffersHit = n
					case kind == "shared" && c[1] == "read":
						s.buffersRead = n
					case kind == "temp" && c[1] == "read":
						s.tempRead = n
					}
				}
			}
		}
	}
	return s
}

// printReport prints results as a Markdown table, then their plans.
func printReport(out io.Writer, results []result, explain bool) {
	fmt.Fprintf(out, "Each call ran once to warm up, once with auto_explain, then %d times timed. "+
		"Buffers, temp blocks and rows read are summed over the call's statements: shared buffers hit in Postgres's cache + read from the OS, "+
		"temporary blocks read back from sorts and hashes spilled to disk (8 kB each), and rows the table and index scans produced or filtered out.\n\n", runs)
	fmt.Fprintln(out, "| Call | Rows | Median | Slowest | Statements | Buffers (hit + read) | Temp blocks | Rows read |")
	fmt.Fprintln(out, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, r := range results {
		statements, buffers, temp, rowsRead := "–", "–", "–", "–"
		if explain {
			var total planStats
			for _, p := range r.plans {
				s := statsOf(p.text)
				total.buffersHit += s.buffersHit
				total.buffersRead += s.buffersRead
				total.tempRead += s.tempRead
				total.rowsRead += s.rowsRead
			}
			statements = strconv.Itoa(len(r.plans))
			buffers = fmt.Sprintf("%s + %s", thousands(total.buffersHit), thousands(total.buffersRead))
			temp = thousands(total.tempRead)
			rowsRead = thousands(total.rowsRead)
		}
		fmt.Fprintf(out, "| %s | %d | %s | %s | %s | %s | %s | %s |\n", r.name, r.rows, ms(r.median), ms(r.slowest), statements, buffers, temp, rowsRead)
	}
	if !explain {
		return
	}

	fmt.Fprintln(out, "\n## Plans")
	for _, r := range results {
		fmt.Fprintf(out, "\n### %s\n", r.name)
		for i, p := range r.plans {
			fmt.Fprintf(out, "\nStatement %d of %d (%s):\n\n```\n%s\n```\n", i+1, len(r.plans), p.duration, p.text)
		}
	}
}

// ms formats d in milliseconds with one decimal.
func ms(d time.Duration) string {
	return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
}
