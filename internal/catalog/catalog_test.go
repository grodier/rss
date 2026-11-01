package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	t.Run("headings and entries in order", func(t *testing.T) {
		in := "# a comment\n\n[News]\nnpr.org\n\n  www.BBC.co.uk/news  \n[Science]\n\nquantamagazine.org\n"
		got, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		want := []Entry{
			{Topic: "News", URL: "https://npr.org/", SiteKey: "npr.org", Position: 0},
			{Topic: "News", URL: "https://www.bbc.co.uk/news", SiteKey: "bbc.co.uk", Position: 1},
			{Topic: "Science", URL: "https://quantamagazine.org/", SiteKey: "quantamagazine.org", Position: 2},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
	})

	errCases := []struct {
		name, in, want string
	}{
		{"address before any heading", "npr.org\n[News]\nbbc.co.uk\n", "before any [Topic] heading"},
		{"not an address", "[News]\nnot an address\n", "isn't a website address"},
		{"address ParseInput rejects", "[News]\nftp://example.com\n", "isn't a website address"},
		{"same site key", "[News]\nexample.com\n[Other]\nwww.example.com/news\n", "same site (example.com) as line 2"},
		{"empty topic", "[News]\n[Science]\nnasa.gov\n", `topic "News" has no sites`},
		{"empty last topic", "[News]\nnpr.org\n[Science]\n\n", `topic "Science" has no sites`},
		{"topic twice", "[News]\nnpr.org\n[News]\nbbc.co.uk\n", `topic "News" appears twice`},
		{"unclosed heading", "[News\nnpr.org\n", "isn't a [Topic] heading"},
		{"empty heading", "[ ]\nnpr.org\n", "isn't a [Topic] heading"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v; want one containing %q", err, tc.want)
			}
		})
	}

	t.Run("empty file", func(t *testing.T) {
		got, err := Parse(strings.NewReader("\n# nothing yet\n"))
		if err != nil || len(got) != 0 {
			t.Errorf("got %v, %v; want no entries and no error", got, err)
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("browse entries, then seed entries, numbered across both", func(t *testing.T) {
		got, err := Load(strings.NewReader("[News]\nnpr.org\n"), strings.NewReader("[News]\nbbc.co.uk\n[Food]\neater.com\n"))
		if err != nil {
			t.Fatal(err)
		}
		want := []Entry{
			{Topic: "News", URL: "https://npr.org/", SiteKey: "npr.org", Position: 0, Browse: true},
			{Topic: "News", URL: "https://bbc.co.uk/", SiteKey: "bbc.co.uk", Position: 1},
			{Topic: "Food", URL: "https://eater.com/", SiteKey: "eater.com", Position: 2},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("empty seed list", func(t *testing.T) {
		got, err := Load(strings.NewReader("[News]\nnpr.org\n"), strings.NewReader("# nothing yet\n"))
		if err != nil || len(got) != 1 {
			t.Errorf("got %v, %v; want npr.org alone", got, err)
		}
	})

	errCases := []struct {
		name, browse, seed, want string
	}{
		{"site in both lists", "[News]\nnpr.org\n", "[Radio]\nwww.npr.org/sections/news\n", "seed.txt: npr.org is also in browse.txt"},
		{"bad browse list", "npr.org\n", "", "browse.txt: line 1"},
		{"bad seed list", "[News]\nnpr.org\n", "[Empty]\n", "seed.txt: line 1"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tc.browse), strings.NewReader(tc.seed))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v; want one containing %q", err, tc.want)
			}
		})
	}
}

// TestEmbeddedCatalog stops a bad edit to browse.txt or seed.txt from
// shipping.
func TestEmbeddedCatalog(t *testing.T) {
	entries := Entries()
	browse := 0
	for _, e := range entries {
		if e.Browse {
			browse++
		}
	}
	if browse == 0 {
		t.Fatal("browse.txt has no entries")
	}
}

type fakeStore struct {
	got        []Entry
	retryAfter time.Duration
	res        Result
	err        error
}

func (f *fakeStore) Replace(_ context.Context, entries []Entry, retryAfter time.Duration) (Result, error) {
	f.got, f.retryAfter = entries, retryAfter
	return f.res, f.err
}

func TestSync(t *testing.T) {
	entries := Entries()
	want := Result{Queued: 3, Retried: []string{"npr.org"}, Missing: []string{"bbc.co.uk"}}
	store := &fakeStore{res: want}
	got, err := Sync(t.Context(), store, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sync = %+v; want %+v", got, want)
	}
	if store.retryAfter != RetryAfter {
		t.Errorf("Replace got retryAfter %v; want %v", store.retryAfter, RetryAfter)
	}
	if !reflect.DeepEqual(store.got, entries) {
		t.Errorf("Replace got %d entries; want all %d", len(store.got), len(entries))
	}

	boom := errors.New("boom")
	if _, err := Sync(t.Context(), &fakeStore{err: boom}, entries); !errors.Is(err, boom) {
		t.Errorf("err = %v; want boom", err)
	}
}
