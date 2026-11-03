package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// encodeCursor formats c for a URL: the UTC time in RFC 3339 with
// nanoseconds, "_", and the ID.
func encodeCursor(c rss.ArticleCursor) string {
	return c.At.UTC().Format(time.RFC3339Nano) + "_" + c.ID
}

// decodeCursor parses encodeCursor's output. "" gives the zero cursor.
// Anything else that isn't a valid time and a UUID is an error.
func decodeCursor(s string) (rss.ArticleCursor, error) {
	if s == "" {
		return rss.ArticleCursor{}, nil
	}
	parts := strings.Split(s, "_")
	if len(parts) != 2 {
		return rss.ArticleCursor{}, fmt.Errorf("cursor %q: want time_id", s)
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return rss.ArticleCursor{}, fmt.Errorf("cursor time: %w", err)
	}
	id := parts[1]
	if !validator.Matches(id, validator.UUIDRX) {
		return rss.ArticleCursor{}, fmt.Errorf("cursor id %q is not a UUID", id)
	}
	return rss.ArticleCursor{At: t.UTC(), ID: strings.ToLower(id)}, nil
}
