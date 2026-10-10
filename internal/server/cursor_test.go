package server

import (
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

func TestEncodeDecodeCursor(t *testing.T) {
	const id = "123e4567-e89b-12d3-a456-426614174000"

	t.Run("round trip keeps nanoseconds", func(t *testing.T) {
		in := rss.ArticleCursor{At: time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC), ID: id}
		got, err := decodeCursor(encodeCursor(in))
		if err != nil {
			t.Fatalf("decodeCursor: %v", err)
		}
		if !got.At.Equal(in.At) || got.ID != in.ID {
			t.Errorf("got %+v, want %+v", got, in)
		}
	})

	t.Run("empty is the zero cursor", func(t *testing.T) {
		got, err := decodeCursor("")
		if err != nil || !got.IsZero() {
			t.Errorf("got %+v, %v; want zero cursor", got, err)
		}
	})

	for name, in := range map[string]string{
		"no underscore": "2026-03-04T05:06:07Z" + id,
		"bad time":      "yesterday_" + id,
		"bad uuid":      "2026-03-04T05:06:07Z_not-a-uuid",
		"extra parts":   "2026-03-04T05:06:07Z_" + id + "_x",
	} {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := decodeCursor(in); err == nil {
				t.Errorf("decodeCursor(%q) = nil error", in)
			}
		})
	}
}
