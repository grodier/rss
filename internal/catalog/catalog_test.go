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

	shapes := []struct {
		name, in string
		want     []Entry
	}{
		{"empty file", "", nil},
		{"only comments", "\n# nothing yet\n", nil},
		{"one site, no topic", "npr.org\n", []Entry{
			{URL: "https://npr.org/", SiteKey: "npr.org", Position: 0},
		}},
		{"sites, no topics", "npr.org\neater.com\n", []Entry{
			{URL: "https://npr.org/", SiteKey: "npr.org", Position: 0},
			{URL: "https://eater.com/", SiteKey: "eater.com", Position: 1},
		}},
		{"a topic with no sites yet", "[News]\n", nil},
		{"sites before the first topic have none", "npr.org\n[Food]\neater.com\n", []Entry{
			{URL: "https://npr.org/", SiteKey: "npr.org", Position: 0},
			{Topic: "Food", URL: "https://eater.com/", SiteKey: "eater.com", Position: 1},
		}},
		{"empty topics between others", "[News]\nnpr.org\n[Sports]\n\n[Food]\neater.com\n[Music]\n", []Entry{
			{Topic: "News", URL: "https://npr.org/", SiteKey: "npr.org", Position: 0},
			{Topic: "Food", URL: "https://eater.com/", SiteKey: "eater.com", Position: 1},
		}},
		{"a feed's address", "[News]\nhttps://feeds.npr.org/1001/rss.xml\n", []Entry{
			{Topic: "News", URL: "https://feeds.npr.org/1001/rss.xml", SiteKey: "feeds.npr.org", Position: 0},
		}},
	}
	for _, tc := range shapes {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}

	errCases := []struct {
		name, in, want string
	}{
		{"not an address", "[News]\nnot an address\n", "line 2: \"not an address\" isn't a website address"},
		{"address ParseInput rejects", "[News]\nftp://example.com\n", "isn't a website address"},
		{"same site key", "[News]\nexample.com\n[Other]\nwww.example.com/news\n", "same site (example.com) as line 2"},
		{"same site twice without topics", "npr.org\nnpr.org/sections/news\n", "same site (npr.org) as line 1"},
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

	t.Run("both lists empty", func(t *testing.T) {
		got, err := Load(strings.NewReader(""), strings.NewReader("[News]\n"))
		if err != nil || len(got) != 0 {
			t.Errorf("got %v, %v; want no entries", got, err)
		}
	})

	errCases := []struct {
		name, browse, seed, want string
	}{
		{"site in both lists", "[News]\nnpr.org\n", "[Radio]\nwww.npr.org/sections/news\n", "seed.txt: npr.org is also in browse.txt"},
		{"bad browse list", "[News\n", "", "browse.txt: line 1"},
		{"bad seed list", "[News]\nnpr.org\n", "[Empty\n", "seed.txt: line 1"},
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
// shipping: Entries panics if they don't load. Either may be empty.
func TestEmbeddedCatalog(t *testing.T) {
	Entries()
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
