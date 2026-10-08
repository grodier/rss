package psql

import (
	"context"
	"database/sql"
	"errors"

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
func (r *SubscriptionRepository) Subscribe(ctx context.Context, userID, feedID string) error {
	stmt := `INSERT INTO subscriptions (user_id, feed_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`

	if _, err := r.DB.ExecContext(ctx, stmt, userID, feedID); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return rss.ErrNoRecord
		}
		return err
	}
	return nil
}

// Unsubscribe removes userID's subscription to feedID. Not an error if there is none.
func (r *SubscriptionRepository) Unsubscribe(ctx context.Context, userID, feedID string) error {
	stmt := `DELETE FROM subscriptions WHERE user_id = $1 AND feed_id = $2`

	_, err := r.DB.ExecContext(ctx, stmt, userID, feedID)
	return err
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

// ListByUser returns userID's subscriptions ordered by site (title, else host),
// then feed title and URL.
func (r *SubscriptionRepository) ListByUser(ctx context.Context, userID string) ([]rss.Subscription, error) {
	stmt := `SELECT ` + qualifiedFeedColumns + `,
			s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at, latest.at
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
		ORDER BY lower(COALESCE(NULLIF(s.title, ''), s.host)), s.host, lower(f.title), f.url`

	rows, err := r.DB.QueryContext(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []rss.Subscription
	for rows.Next() {
		var sub rss.Subscription
		var latest sql.NullTime
		// scanFeed only takes a Scan func, so wrap the extra columns around it.
		feed, err := scanFeed(scanFunc(func(dest ...any) error {
			dest = append(dest, &sub.Site.ID, &sub.Site.Host, &sub.Site.URL, &sub.Site.Title,
				&sub.Site.Description, &sub.Site.CreatedAt, &sub.Site.UpdatedAt, &latest)
			return rows.Scan(dest...)
		}))
		if err != nil {
			return nil, err
		}
		sub.Feed = feed
		sub.LatestArticleAt = latest.Time
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return subs, nil
}

// scanFunc adapts a function to the Scan interface scanFeed takes.
type scanFunc func(dest ...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }
