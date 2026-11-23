package server

// searchBox is a page's search box, rendered by the search-box partial
// (partials/search_box.html) inside the page's own form, so Discover's,
// My feeds' and Saved's boxes are the same markup: the magnifier submits,
// and a clear button at the field's end links to ClearURL, the page without
// the query, so it works without JavaScript (search-clear.ts makes it clear
// in place where the page updates as you type).
type searchBox struct {
	// ID is the input's id; its error, when it has one, is ID-error.
	ID          string
	Label       string
	Placeholder string
	Value       string
	// ClearURL is the page as it is without the query.
	ClearURL string
	// Prose keeps spellcheck, autocorrect and auto-capitalization on, for
	// a box that searches article text (Saved) rather than names and
	// addresses (see "Search" in docs/decisions.md).
	Prose bool
	// InPlace says the page updates as the reader types (live search, My
	// feeds' filter), so clearing the box needs no request of its own.
	InPlace bool
	// Error is the query's field error, shown after the box.
	Error string
}
