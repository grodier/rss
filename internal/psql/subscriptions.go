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
