package server

import "net/http"

// designOptions holds the switches of #424's design checkpoint. Temporary:
// the options not picked, and this file, are removed before the PR is
// ready for review.
//
// Set them with query parameters on any logged-in page:
// ?rows=a|b|c, ?headers=a|b and ?heads=pills|links. The defaults are the
// preferred options (a, a, pills).
type designOptions struct {
	Rows    string // a: the row is the link; b: an overflow menu; c: actions under the meta line
	Headers string // a: site headers only for 2+ subscriptions; b: no site headers
	Heads   string // pills or links
}

func designFrom(r *http.Request) designOptions {
	q := r.URL.Query()
	pick := func(key, def string, allowed ...string) string {
		v := q.Get(key)
		for _, a := range allowed {
			if v == a {
				return v
			}
		}
		return def
	}
	return designOptions{
		Rows:    pick("rows", "a", "a", "b", "c"),
		Headers: pick("headers", "a", "a", "b"),
		Heads:   pick("heads", "pills", "pills", "links"),
	}
}
