package server

import (
	"fmt"
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
		if got.Group != 0 || !got.At.Equal(in.At) || got.ID != in.ID {
			t.Errorf("got %+v, want %+v", got, in)
		}
	})

	for _, group := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("round trip keeps group %d", group), func(t *testing.T) {
			in := rss.ArticleCursor{Group: group, At: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), ID: id}
			got, err := decodeCursor(encodeCursor(in))
			if err != nil {
				t.Fatalf("decodeCursor: %v", err)
			}
			if got.Group != group || !got.At.Equal(in.At) || got.ID != in.ID {
				t.Errorf("got %+v, want %+v", got, in)
			}
		})
	}

	t.Run("empty is the zero cursor", func(t *testing.T) {
		got, err := decodeCursor("")
		if err != nil || !got.IsZero() {
			t.Errorf("got %+v, %v; want zero cursor", got, err)
		}
	})

	for name, in := range map[string]string{
		"no underscore":      "0" + "2026-03-04T05:06:07Z" + id,
		"missing group":      "2026-03-04T05:06:07Z_" + id,
		"group too high":     "3_2026-03-04T05:06:07Z_" + id,
		"negative group":     "-1_2026-03-04T05:06:07Z_" + id,
		"group not a number": "x_2026-03-04T05:06:07Z_" + id,
		"bad time":           "0_yesterday_" + id,
		"bad uuid":           "0_2026-03-04T05:06:07Z_not-a-uuid",
		"extra parts":        "0_2026-03-04T05:06:07Z_" + id + "_x",
	} {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := decodeCursor(in); err == nil {
				t.Errorf("decodeCursor(%q) = nil error", in)
			}
		})
	}
}

func TestEncodeDecodeAnchors(t *testing.T) {
	look := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
	for name, in := range map[string]rss.TimelineAnchors{
		"all set":      {Look: look, Visit: look.Add(-time.Hour), Line: look.Add(-2 * time.Hour)},
		"no look":      {Visit: look.Add(-time.Hour), Line: look.Add(-2 * time.Hour)},
		"only a look":  {Look: look},
		"zero anchors": {},
	} {
		t.Run("round trip: "+name, func(t *testing.T) {
			got, err := decodeAnchors(encodeAnchors(in))
			if err != nil {
				t.Fatalf("decodeAnchors: %v", err)
			}
			if !got.Look.Equal(in.Look) || !got.Visit.Equal(in.Visit) || !got.Line.Equal(in.Line) ||
				got.Look.IsZero() != in.Look.IsZero() || got.Visit.IsZero() != in.Visit.IsZero() || got.Line.IsZero() != in.Line.IsZero() {
				t.Errorf("got %+v, want %+v", got, in)
			}
		})
	}

	t.Run("zero anchors aren't empty", func(t *testing.T) {
		// The pager always carries them, so a later page never falls back to
		// the mark.
		if got := encodeAnchors(rss.TimelineAnchors{}); got == "" {
			t.Errorf("encodeAnchors(zero) = %q", got)
		}
	})

	for name, in := range map[string]string{
		"empty":       "",
		"too few":     "_",
		"too many":    "___",
		"bad time":    "yesterday__",
		"a time only": "2026-03-04T05:06:07Z",
	} {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := decodeAnchors(in); err == nil {
				t.Errorf("decodeAnchors(%q) = nil error", in)
			}
		})
	}
}
