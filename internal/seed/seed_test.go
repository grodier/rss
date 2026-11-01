package seed

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	shapes := []struct {
		name, in string
		want     []Entry
	}{
		{"empty file", "", nil},
		{"only comments and blank lines", "\n# nothing yet\n\n", nil},
		{"one site", "npr.org\n", []Entry{{URL: "https://npr.org/", SiteKey: "npr.org"}}},
		{"sites in order, trimmed, with comments between", "  www.BBC.co.uk/news  \n# food\neater.com\n", []Entry{
			{URL: "https://www.bbc.co.uk/news", SiteKey: "bbc.co.uk"},
			{URL: "https://eater.com/", SiteKey: "eater.com"},
		}},
		{"a feed's address", "https://feeds.npr.org/1001/rss.xml\n", []Entry{
			{URL: "https://feeds.npr.org/1001/rss.xml", SiteKey: "feeds.npr.org"},
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
		{"not an address", "npr.org\nnot an address\n", `line 2: "not an address" isn't a website address`},
		{"address ParseInput rejects", "ftp://example.com\n", "isn't a website address"},
		{"same site twice", "example.com\n\nwww.example.com/news\n", "same site (example.com) as line 1"},
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

// TestEmbeddedList stops a bad edit to sites.txt from shipping: Entries
// panics if it doesn't parse. It may be empty.
func TestEmbeddedList(t *testing.T) {
	Entries()
}

type fakeStore struct {
	got        []Entry
	retryAfter time.Duration
	res        Result
	err        error
}

func (f *fakeStore) Queue(_ context.Context, entries []Entry, retryAfter time.Duration) (Result, error) {
	f.got, f.retryAfter = entries, retryAfter
	return f.res, f.err
}

func TestRun(t *testing.T) {
	entries := []Entry{{URL: "https://npr.org/", SiteKey: "npr.org"}}
	want := Result{Queued: 3, Retried: []string{"npr.org"}, Missing: []string{"bbc.co.uk"}}
	store := &fakeStore{res: want}
	got, err := Run(t.Context(), store, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Run = %+v; want %+v", got, want)
	}
	if store.retryAfter != RetryAfter || !reflect.DeepEqual(store.got, entries) {
		t.Errorf("Queue got %v, %v; want the entries and %v", store.got, store.retryAfter, RetryAfter)
	}

	boom := errors.New("boom")
	if _, err := Run(t.Context(), &fakeStore{err: boom}, entries); !errors.Is(err, boom) {
		t.Errorf("err = %v; want boom", err)
	}
}
