package rss

// Section is one of Discover's sections, a discover_sections row. A feed
// in Discover's catalog is under one of them; sections belong to Discover
// only, never to a reader's subscriptions ("First run and discovery" in
// docs/decisions.md). The rows are the list: code never names a section.
type Section struct {
	ID   string
	Slug string // in URLs: "film-and-tv"
	Name string // as shown: "Film and TV"
}
