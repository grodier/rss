package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type SubscriptionRepository struct {
	DB *sql.DB
}

func NewSubscriptionRepository(db *sql.DB) *SubscriptionRepository {
	return &SubscriptionRepository{DB: db}
}

// Subscribe subscribes userID to feedID. Subscribing twice is not an error.
// rss.ErrNoRecord if the feed (or user) doesn't exist.
//
// A new subscription makes the feed's posts from the last rss.NewsWindow new
// to the reader: if they have a timeline mark, each article positioned within
// rss.NewsWindow of now gets their own position, a timeline_entries row, its
// timeline_at raised above what they've seen (see raisePositions). Older
// posts stay at their shared positions. An OPML import subscribes through
// this too.
func (r *SubscriptionRepository) Subscribe(ctx context.Context, userID, feedID string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `INSERT INTO subscriptions (user_id, feed_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`

	res, err := tx.ExecContext(ctx, stmt, userID, feedID)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return rss.ErrNoRecord
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // already subscribed: their positions are set
	}

	var seenUpTo, now time.Time
	err = tx.QueryRowContext(ctx, `SELECT seen_up_to, now() FROM timeline_marks WHERE user_id = $1`, userID).Scan(&seenUpTo, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit() // no mark: shared positions
	}
	if err != nil {
		return err
	}

	recentStmt := `SELECT id, timeline_at
		FROM articles
		WHERE feed_id = $1 AND timeline_at > now() - make_interval(secs => $2)
		ORDER BY timeline_at DESC, id DESC`

	rows, err := tx.QueryContext(ctx, recentStmt, feedID, int(rss.NewsWindow.Seconds()))
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	var positions []time.Time
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return err
		}
		ids = append(ids, id)
		positions = append(positions, at)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	var entries timelineEntries
	entries.add(userID, ids, raisePositions(positions, seenUpTo, now))
	if err := entries.insert(ctx, tx, feedID); err != nil {
		return err
	}
	return tx.Commit()
}

// Unsubscribe removes userID's subscription to feedID, and their own
// positions for its articles (their timeline_entries rows), in one
// transaction. Not an error if there is none.
func (r *SubscriptionRepository) Unsubscribe(ctx context.Context, userID, feedID string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, userID, feedID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM timeline_entries WHERE user_id = $1 AND feed_id = $2`, userID, feedID); err != nil {
		return err
	}
	return tx.Commit()
}

// SubscribedFeedIDs reports which of feedIDs userID is subscribed to.
// An empty feedIDs returns an empty map without querying.
func (r *SubscriptionRepository) SubscribedFeedIDs(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
	subscribed := map[string]bool{}
	if len(feedIDs) == 0 {
		return subscribed, nil
	}

	stmt := `SELECT feed_id FROM subscriptions WHERE user_id = $1 AND feed_id = ANY($2)`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, pq.Array(feedIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		subscribed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return subscribed, nil
}

// subscriptionColumns are the columns scanSubscription reads, after the feed's.
const subscriptionColumns = `sub.title, s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at`

// Get returns userID's subscription to feedID, with its feed and site.
// LatestArticleAt is left zero. rss.ErrNoRecord if userID doesn't
// subscribe to feedID.
func (r *SubscriptionRepository) Get(ctx context.Context, userID, feedID string) (rss.Subscription, error) {
	stmt := `SELECT ` + qualifiedFeedColumns + `, ` + subscriptionColumns + `, NULL::timestamptz
		FROM subscriptions sub
		JOIN feeds f ON f.id = sub.feed_id
		JOIN sites s ON s.id = f.site_id
		WHERE sub.user_id = $1 AND sub.feed_id = $2`

	sub, err := scanSubscription(r.DB.QueryRowContext(ctx, stmt, userID, feedID))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Subscription{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.Subscription{}, err
	}

	tags, err := r.tagsByFeed(ctx, userID, sql.NullString{String: feedID, Valid: true})
	if err != nil {
		return rss.Subscription{}, err
	}
	sub.Tags = tags[feedID]
	return sub, nil
}

// Rename sets userID's name for feedID; "" uses the feed's own title.
// rss.ErrNoRecord if userID doesn't subscribe to feedID.
func (r *SubscriptionRepository) Rename(ctx context.Context, userID, feedID, title string) error {
	stmt := `UPDATE subscriptions SET title = $3 WHERE user_id = $1 AND feed_id = $2`

	res, err := r.DB.ExecContext(ctx, stmt, userID, feedID, title)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return rss.ErrNoRecord
	}
	return nil
}

// ListByUser returns userID's subscriptions ordered by site (title, else host),
// then feed name (the user's, else the feed's title) and URL.
func (r *SubscriptionRepository) ListByUser(ctx context.Context, userID string) ([]rss.Subscription, error) {
	stmt := `SELECT ` + qualifiedFeedColumns + `, ` + subscriptionColumns + `, latest.at
		FROM subscriptions sub
		JOIN feeds f ON f.id = sub.feed_id
		JOIN sites s ON s.id = f.site_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(a.published_at, a.created_at) AS at
			FROM articles a
			WHERE a.feed_id = f.id
			ORDER BY COALESCE(a.published_at, a.created_at) DESC
			LIMIT 1
		) latest ON true
		WHERE sub.user_id = $1
		ORDER BY lower(COALESCE(NULLIF(s.title, ''), s.host)), s.host,
			lower(COALESCE(NULLIF(sub.title, ''), f.title)), f.url`

	rows, err := r.DB.QueryContext(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []rss.Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tags, err := r.tagsByFeed(ctx, userID, sql.NullString{})
	if err != nil {
		return nil, err
	}
	for i := range subs {
		subs[i].Tags = tags[subs[i].Feed.ID]
	}

	return subs, nil
}

// tagsByFeed returns the tags of userID's subscriptions, by feed, each
// feed's ordered by name ignoring case. A NULL feedID means every
// subscription; otherwise only that feed's.
func (r *SubscriptionRepository) tagsByFeed(ctx context.Context, userID string, feedID sql.NullString) (map[string][]rss.Tag, error) {
	// name is citext, so ordering by it ignores case.
	stmt := `SELECT st.feed_id, t.id, t.name
		FROM subscription_tags st
		JOIN tags t ON t.id = st.tag_id
		WHERE st.user_id = $1 AND ($2::uuid IS NULL OR st.feed_id = $2::uuid)
		ORDER BY t.name`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tags := map[string][]rss.Tag{}
	for rows.Next() {
		var feedID string
		var tag rss.Tag
		if err := rows.Scan(&feedID, &tag.ID, &tag.Name); err != nil {
			return nil, err
		}
		tags[feedID] = append(tags[feedID], tag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tags, nil
}

// scanSubscription scans a row selected with qualifiedFeedColumns,
// subscriptionColumns and the latest article's time.
func scanSubscription(row interface{ Scan(...any) error }) (rss.Subscription, error) {
	var sub rss.Subscription
	var latest sql.NullTime
	// scanFeed only takes a Scan func, so wrap the extra columns around it.
	feed, err := scanFeed(scanFunc(func(dest ...any) error {
		dest = append(dest, &sub.Title, &sub.Site.ID, &sub.Site.Host, &sub.Site.URL, &sub.Site.Title,
			&sub.Site.Description, &sub.Site.CreatedAt, &sub.Site.UpdatedAt, &latest)
		return row.Scan(dest...)
	}))
	if err != nil {
		return rss.Subscription{}, err
	}
	sub.Feed = feed
	sub.LatestArticleAt = latest.Time
	return sub, nil
}

// scanFunc adapts a function to the Scan interface scanFeed takes.
type scanFunc func(dest ...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }
