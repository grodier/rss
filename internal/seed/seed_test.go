package seed

import (
	"reflect"
	"strings"
	"testing"
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
