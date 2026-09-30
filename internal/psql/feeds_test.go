package psql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
)

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryCreateDuplicate(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)

	url := fmt.Sprintf("https://example.com/feed-%d.xml", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
			t.Errorf("cleanup feed: %v", err)
		}
	})

	if _, err := repo.Create(Feed{Url: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.Create(Feed{Url: url})
	if !errors.Is(err, ErrDuplicateFeed) {
		t.Fatalf("got %v, want ErrDuplicateFeed", err)
	}
}
