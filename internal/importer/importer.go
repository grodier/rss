// Package importer subscribes users to the feeds of their OPML imports,
// worked from the import_feeds queue.
package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/lookup"
	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers       = 6
	defaultPollInterval  = time.Second
	defaultJobTimeout    = 30 * time.Second
	defaultStaleAfter    = 2 * time.Minute
	defaultMaxAttempts   = 3
	defaultResetInterval = time.Minute

	// recordTimeout bounds writing a feed's outcome, which uses a fresh
	// context so an expired job context doesn't prevent it.
	recordTimeout = 5 * time.Second

	// nameMaxChars is the longest name a subscription can have, as for a
	// name typed on Edit subscription (subscriptionTitleMaxChars).
	nameMaxChars = 100
)

// Store is the import queue; satisfied by *psql.ImportRepository.
type Store interface {
	ClaimNext(ctx context.Context) (rss.ImportFeed, error) // rss.ErrNoRecord when empty
	Finish(ctx context.Context, f rss.ImportFeed) error
	ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error)
}

// Feeds finds known feeds; satisfied by *psql.FeedRepository.
type Feeds interface {
	GetByURL(ctx context.Context, url string) (rss.Feed, error) // rss.ErrNoRecord if unknown
}

// Subscriptions subscribes users to feeds; satisfied by
// *psql.SubscriptionRepository.
type Subscriptions interface {
	Subscribe(ctx context.Context, userID, feedID string) error
	Rename(ctx context.Context, userID, feedID, title string) error
	SubscribedFeedIDs(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error)
}

// Tags tags subscriptions; satisfied by *psql.TagRepository.
type Tags interface {
	AddToSubscription(ctx context.Context, userID, feedID string, names []string) error
}

// Saver stores a discovered site and its feeds; satisfied by
// *psql.DiscoveryRepository.
type Saver interface {
	Save(ctx context.Context, site rss.Site, feeds []rss.FeedWithArticles) (string, error)
}

// Discoverer finds a site's feeds; satisfied by *discovery.Discoverer.
type Discoverer interface {
	Discover(ctx context.Context, u *url.URL) (discovery.Result, error)
}

// Runner processes queued import feeds with a pool of workers. Zero values
// use the defaults.
type Runner struct {
	Store         Store
	Feeds         Feeds
	Subscriptions Subscriptions
	Tags          Tags
	Saver         Saver
	Discoverer    Discoverer
	Logger        *slog.Logger
	Workers       int           // default 6
	PollInterval  time.Duration // default 1s
	JobTimeout    time.Duration // default 30s
	StaleAfter    time.Duration // default 2m (must exceed JobTimeout)
	MaxAttempts   int           // default 3
	ResetInterval time.Duration // default 1m; how often stale jobs are reset
}

// Run resets stale jobs, then runs Workers goroutines and resets stale jobs
// again every ResetInterval until ctx is canceled, and returns after they
// have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.StaleAfter <= r.JobTimeout {
		return fmt.Errorf("importer: StaleAfter (%s) must exceed JobTimeout (%s)", r.StaleAfter, r.JobTimeout)
	}

	r.resetStale(ctx)

	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(r.ResetInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.resetStale(ctx)
			}
		}
	})
	for range r.Workers {
		wg.Go(func() { r.work(ctx) })
	}
	wg.Wait()
	return nil
}

func (r *Runner) setDefaults() {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Workers <= 0 {
		r.Workers = defaultWorkers
	}
	if r.PollInterval <= 0 {
		r.PollInterval = defaultPollInterval
	}
	if r.JobTimeout <= 0 {
		r.JobTimeout = defaultJobTimeout
	}
	if r.StaleAfter <= 0 {
		r.StaleAfter = defaultStaleAfter
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = defaultMaxAttempts
	}
	if r.ResetInterval <= 0 {
		r.ResetInterval = defaultResetInterval
	}
}

// resetStale puts running feeds that started more than StaleAfter ago back
// to pending, or fails them after MaxAttempts (see Store.ResetStale), and
// logs what it changed.
func (r *Runner) resetStale(ctx context.Context) {
	n, err := r.Store.ResetStale(ctx, r.StaleAfter, r.MaxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			r.Logger.Error("importer: reset stale jobs", "error", err)
		}
		return
	}
	if n > 0 {
		r.Logger.Info("importer: reset stale jobs", "count", n)
	}
}

func (r *Runner) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		f, err := r.Store.ClaimNext(ctx)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("importer: claim next job", "error", err)
			}
			if !r.wait(ctx) {
				return
			}
			continue
		}

		r.process(ctx, f)
	}
}

// wait sleeps for PollInterval and reports whether ctx is still live.
func (r *Runner) wait(ctx context.Context) bool {
	t := time.NewTimer(r.PollInterval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// process imports one feed and records its outcome, unless ctx was canceled
// meanwhile: the row is then left running, and is re-queued once it's stale.
func (r *Runner) process(ctx context.Context, f rss.ImportFeed) {
	start := time.Now()
	f = r.run(ctx, f)

	if ctx.Err() != nil {
		r.Logger.Info("import feed interrupted by shutdown", "import_id", f.ImportID, "position", f.Position)
		return
	}

	if f.Error != "" {
		r.Logger.Info("import feed failed", "import_id", f.ImportID, "position", f.Position, "failure", f.Failure, "error", f.Error)
	}

	recCtx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	if err := r.Store.Finish(recCtx, f); err != nil {
		r.Logger.Error("importer: record result", "import_id", f.ImportID, "position", f.Position, "error", err)
	}

	r.Logger.Info("import feed finished",
		"import_id", f.ImportID,
		"position", f.Position,
		"status", f.Status,
		"duration", time.Since(start),
	)
}

// run finds or discovers f's feed, subscribes f's user to it, names a new
// subscription after f's title and adds f's tags, and returns f with its outcome set. A panic is a failure.
func (r *Runner) run(ctx context.Context, f rss.ImportFeed) (out rss.ImportFeed) {
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("importer: panic", "import_id", f.ImportID, "position", f.Position, "panic", rec)
			out = failed(f, rss.ImportUnreachable, fmt.Errorf("panic: %v", rec))
		}
	}()

	u, err := discovery.ParseInput(f.URL)
	if err != nil {
		return failed(f, rss.ImportInvalidURL, nil)
	}

	jobCtx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	defer cancel()

	feed, err := r.Feeds.GetByURL(jobCtx, discovery.CanonicalFeedURL(u))
	switch {
	case err == nil && feed.GoneAt.IsZero():
		// Known: subscribe to it directly.
	case err == nil || errors.Is(err, rss.ErrNoRecord):
		// Unknown or gone: check it, as a lookup would.
		var failure rss.ImportFailure
		feed, failure, err = r.discover(jobCtx, u)
		if failure != "" {
			return failed(f, failure, err)
		}
	default:
		return failed(f, rss.ImportUnreachable, fmt.Errorf("get feed: %w", err))
	}

	f.FeedID = feed.ID
	subscribed, err := r.Subscriptions.SubscribedFeedIDs(jobCtx, f.UserID, []string{feed.ID})
	if err != nil {
		return failed(f, rss.ImportUnreachable, fmt.Errorf("subscribed feeds: %w", err))
	}
	if subscribed[feed.ID] {
		f.Status = rss.ImportAlreadySubscribed
	} else {
		if err := r.Subscriptions.Subscribe(jobCtx, f.UserID, feed.ID); err != nil {
			return failed(f, rss.ImportUnreachable, fmt.Errorf("subscribe: %w", err))
		}
		f.Status = rss.ImportSubscribed
		r.setName(jobCtx, f, feed)
	}
	r.addTags(jobCtx, f)
	return f
}

// addTags adds f's tags to the subscription to f.FeedID. The feed is
// subscribed either way, so a failure is only logged.
func (r *Runner) addTags(ctx context.Context, f rss.ImportFeed) {
	if len(f.Tags) == 0 {
		return
	}
	if err := r.Tags.AddToSubscription(ctx, f.UserID, f.FeedID, f.Tags); err != nil {
		r.Logger.Error("importer: add tags", "import_id", f.ImportID, "position", f.Position, "error", err)
	}
}

// setName gives the subscription to feed the name f's outline gives it, if
// any (see importedName). The feed is subscribed either way, so a failure is
// only logged.
func (r *Runner) setName(ctx context.Context, f rss.ImportFeed, feed rss.Feed) {
	name := importedName(f.Title, f.URL, feed)
	if name == "" {
		return
	}
	if err := r.Subscriptions.Rename(ctx, f.UserID, feed.ID, name); err != nil {
		r.Logger.Error("importer: set name", "import_id", f.ImportID, "position", f.Position, "error", err)
	}
}

// importedName returns the subscription name an outline's title gives
// feed: "" when it's empty, the outline's URL, too long, or the feed's
// own title ignoring case and whitespace.
func importedName(title, outlineURL string, feed rss.Feed) string {
	title = collapseSpace(title)
	switch {
	case title == "",
		title == strings.TrimSpace(outlineURL),
		utf8.RuneCountInString(title) > nameMaxChars,
		strings.EqualFold(title, collapseSpace(feed.Title)):
		return ""
	}
	return title
}

// collapseSpace trims s and replaces each run of whitespace with one space,
// as opml.Parse does.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// discover fetches u, saves what it finds exactly like a lookup, and
// returns the first feed found; a web page's first advertised feed. On
// failure it returns why, and the internal error if there is one.
func (r *Runner) discover(ctx context.Context, u *url.URL) (rss.Feed, rss.ImportFailure, error) {
	res, err := r.Discoverer.Discover(ctx, u)
	if errors.Is(err, discovery.ErrAppLink) {
		return rss.Feed{}, rss.ImportAppLink, err
	}
	if err != nil {
		return rss.Feed{}, rss.ImportUnreachable, err
	}
	if len(res.Feeds) == 0 {
		return rss.Feed{}, rss.ImportNotFeed, nil
	}

	site, feeds := lookup.ToSave(res, time.Now())
	if _, err := r.Saver.Save(ctx, site, feeds); err != nil {
		return rss.Feed{}, rss.ImportUnreachable, fmt.Errorf("save: %w", err)
	}
	feed, err := r.Feeds.GetByURL(ctx, res.Feeds[0].URL)
	if err != nil {
		return rss.Feed{}, rss.ImportUnreachable, fmt.Errorf("get saved feed: %w", err)
	}
	return feed, "", nil
}

// failed returns f failed with failure, recording err's message if any.
func failed(f rss.ImportFeed, failure rss.ImportFailure, err error) rss.ImportFeed {
	f.Status = rss.ImportFailed
	f.Failure = failure
	f.FeedID = ""
	if err != nil {
		f.Error = err.Error()
	}
	return f
}
