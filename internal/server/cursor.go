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
// Anything else that isn't a valid time and UUID is an error.
func decodeCursor(s string) (rss.ArticleCursor, error) {
	if s == "" {
		return rss.ArticleCursor{}, nil
	}
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return rss.ArticleCursor{}, fmt.Errorf("cursor %q: missing \"_\"", s)
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return rss.ArticleCursor{}, fmt.Errorf("cursor time: %w", err)
	}
	if !validator.Matches(id, validator.UUIDRX) {
		return rss.ArticleCursor{}, fmt.Errorf("cursor id %q is not a UUID", id)
	}
	return rss.ArticleCursor{At: t.UTC(), ID: strings.ToLower(id)}, nil
}
