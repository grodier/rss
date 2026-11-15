package rss

// Section is one of Discover's sections. A feed in Discover's catalog is
// under one of them; sections belong to Discover only, never to a
// reader's subscriptions ("First run and discovery" in docs/decisions.md).
type Section struct {
	Name string // as shown: "Film and TV"
	Slug string // in URLs: "film-and-tv"
}

// Sections are Discover's sections, in its default order. The catalog
// stores a section's Name, so renaming one needs a migration of
// discover_feeds.
var Sections = []Section{
	{"News", "news"},
	{"Technology", "technology"},
	{"Programming", "programming"},
	{"Science", "science"},
	{"Business and economics", "business-and-economics"},
	{"Culture", "culture"},
	{"Books", "books"},
	{"Design", "design"},
	{"Food", "food"},
	{"Sport", "sport"},
	{"Games", "games"},
	{"Music", "music"},
	{"Film and TV", "film-and-tv"},
	{"Health", "health"},
	{"Travel", "travel"},
}

// SectionNamed returns the section with that name, and whether there is one.
func SectionNamed(name string) (Section, bool) {
	for _, s := range Sections {
		if s.Name == name {
			return s, true
		}
	}
	return Section{}, false
}
