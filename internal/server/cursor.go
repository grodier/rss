package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// maxCursorGroup is the highest group a cursor can be in: a timeline's
// groups are 0 to 2 (see psql's ListTimeline).
const maxCursorGroup = 2

// encodeCursor formats c for a URL: the group, "_", the UTC time in RFC 3339
// with nanoseconds, "_", and the ID.
func encodeCursor(c rss.ArticleCursor) string {
	return strconv.Itoa(c.Group) + "_" + c.At.UTC().Format(time.RFC3339Nano) + "_" + c.ID
}

// decodeCursor parses encodeCursor's output. "" gives the zero cursor.
// Anything else that isn't a group from 0 to maxCursorGroup, a valid time
// and a UUID is an error.
func decodeCursor(s string) (rss.ArticleCursor, error) {
	if s == "" {
		return rss.ArticleCursor{}, nil
	}
	parts := strings.Split(s, "_")
	if len(parts) != 3 {
		return rss.ArticleCursor{}, fmt.Errorf("cursor %q: want group_time_id", s)
	}
	group, err := strconv.Atoi(parts[0])
	if err != nil || group < 0 || group > maxCursorGroup || parts[0] != strconv.Itoa(group) {
		return rss.ArticleCursor{}, fmt.Errorf("cursor group %q is not 0 to %d", parts[0], maxCursorGroup)
	}
	t, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return rss.ArticleCursor{}, fmt.Errorf("cursor time: %w", err)
	}
	id := parts[2]
	if !validator.Matches(id, validator.UUIDRX) {
		return rss.ArticleCursor{}, fmt.Errorf("cursor id %q is not a UUID", id)
	}
	return rss.ArticleCursor{Group: group, At: t.UTC(), ID: strings.ToLower(id)}, nil
}
