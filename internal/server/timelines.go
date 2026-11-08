package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

const (
	// timelineNameMaxChars is the longest timeline name.
	timelineNameMaxChars = 50
	// timelineDescriptionMaxChars is the longest timeline description.
	timelineDescriptionMaxChars = 280
	// timelineMaxPostsPerWeek is the highest "at most N posts a week".
	timelineMaxPostsPerWeek = 100
	// timelineMaxTerms is the most words or phrases in a keyword field.
	timelineMaxTerms = 20
	// timelineTermMaxChars is the longest word or phrase.
	timelineTermMaxChars = 50
)

type timelineForm struct {
	Name                string   `form:"name"`
	Description         string   `form:"description"`
	Show                string   `form:"show"` // "all" or "some"
	IncludeTags         []string `form:"include_tags"`
	IncludeFeeds        []string `form:"include_feeds"`
	ExcludeTags         []string `form:"exclude_tags"`
	ExcludeFeeds        []string `form:"exclude_feeds"`
	StarredOnly         bool     `form:"starred_only"`
	Quiet               bool     `form:"quiet"`              // "From quiet feeds" is ticked
	MaxPostsPerWeek     string   `form:"max_posts_per_week"` // counts only when Quiet is ticked
	MediaOnly           bool     `form:"media_only"`
	Mentions            bool     `form:"mentions"`      // "Mentioning" is ticked
	IncludeWords        string   `form:"include_words"` // comma-separated; counts only when Mentions is ticked
	NotMentions         bool     `form:"not_mentions"`  // "Not mentioning" is ticked
	ExcludeWords        string   `form:"exclude_words"` // counts only when NotMentions is ticked
	validator.Validator `form:"-"`
}

// defaultMaxPostsPerWeek is the number "From quiet feeds" starts at when
// it isn't ticked, so ticking it begins somewhere sensible.
const defaultMaxPostsPerWeek = "2"

// timelineFormFor returns the form showing t's settings: a condition is
// ticked when it has a value.
func timelineFormFor(t rss.Timeline) timelineForm {
	form := timelineForm{
		Name:            t.Name,
		Description:     t.Description,
		Show:            "all",
		ExcludeTags:     t.Filter.ExcludeTagIDs,
		ExcludeFeeds:    t.Filter.ExcludeFeedIDs,
		StarredOnly:     t.Filter.StarredOnly,
		MaxPostsPerWeek: defaultMaxPostsPerWeek,
		MediaOnly:       t.Filter.MediaOnly,
		Mentions:        len(t.Filter.IncludeWords) > 0,
		IncludeWords:    strings.Join(t.Filter.IncludeWords, ", "),
		NotMentions:     len(t.Filter.ExcludeWords) > 0,
		ExcludeWords:    strings.Join(t.Filter.ExcludeWords, ", "),
	}
	if t.Filter.MaxPostsPerWeek > 0 {
		form.Quiet = true
		form.MaxPostsPerWeek = strconv.Itoa(t.Filter.MaxPostsPerWeek)
	}
	if t.Filter.OnlyIncluded {
		form.Show = "some"
		form.IncludeTags = t.Filter.IncludeTagIDs
		form.IncludeFeeds = t.Filter.IncludeFeedIDs
	}
	return form
}

// check trims the name and description, keeps only the user's tags and
// feeds (and each once), and validates the form. Show must already be "all"
// or "some". A condition's field is validated only when it's ticked: hidden
// fields still post (the reveal is CSS), and an unticked one isn't stored.
func (f *timelineForm) check(tags []rss.Tag, subs []rss.Subscription) {
	f.Name = strings.TrimSpace(f.Name)
	f.Description = strings.TrimSpace(f.Description)
	f.MaxPostsPerWeek = strings.TrimSpace(f.MaxPostsPerWeek)

	tagIDs := map[string]bool{}
	for _, tag := range tags {
		tagIDs[tag.ID] = true
	}
	feedIDs := map[string]bool{}
	for _, sub := range subs {
		feedIDs[sub.Feed.ID] = true
	}
	f.IncludeTags = keepKnown(f.IncludeTags, tagIDs)
	f.ExcludeTags = keepKnown(f.ExcludeTags, tagIDs)
	f.IncludeFeeds = keepKnown(f.IncludeFeeds, feedIDs)
	f.ExcludeFeeds = keepKnown(f.ExcludeFeeds, feedIDs)

	f.CheckField(validator.NotBlank(f.Name), "name", "Enter a name.")
	f.CheckField(validator.MaxChars(f.Name, timelineNameMaxChars), "name", "Must be 50 characters or fewer.")
	f.CheckField(validator.MaxChars(f.Description, timelineDescriptionMaxChars), "description", "Must be 280 characters or fewer.")
	f.CheckField(f.Show == "all" || len(f.IncludeTags) > 0 || len(f.IncludeFeeds) > 0, "show",
		"Choose at least one tag or feed, or show all your feeds.")
	if f.Quiet {
		n, ok := f.maxPostsPerWeek()
		f.CheckField(ok && n > 0, "max_posts_per_week", "Enter a whole number from 1 to 100.")
	}
	if f.Mentions {
		f.checkTerms(splitTerms(f.IncludeWords), "include_words")
	}
	if f.NotMentions {
		f.checkTerms(splitTerms(f.ExcludeWords), "exclude_words")
	}
}

// checkTerms validates the words or phrases of a ticked field.
func (f *timelineForm) checkTerms(terms []string, field string) {
	f.CheckField(len(terms) > 0, field, "Enter at least one word or phrase.")
	f.CheckField(len(terms) <= timelineMaxTerms, field, "At most 20 words or phrases.")
	for _, term := range terms {
		f.CheckField(validator.MaxChars(term, timelineTermMaxChars), field, "Each word or phrase must be 50 characters or fewer.")
		f.CheckField(strings.ContainsFunc(term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }), field,
			"Each word or phrase needs at least one letter or number.")
	}
}

// splitTerms splits a comma-separated list into trimmed terms with inner
// whitespace collapsed, dropping empty ones and repeats (ignoring case).
func splitTerms(s string) []string {
	var terms []string
	seen := map[string]bool{}
	for part := range strings.SplitSeq(s, ",") {
		term := strings.Join(strings.Fields(part), " ")
		key := strings.ToLower(term)
		if term == "" || seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, term)
	}
	return terms
}

// maxPostsPerWeek returns MaxPostsPerWeek as a number: 0 when it's blank.
// ok is false unless it's blank or a whole number from 1 to 100.
func (f *timelineForm) maxPostsPerWeek() (n int, ok bool) {
	if f.MaxPostsPerWeek == "" {
		return 0, true
	}
	n, err := strconv.Atoi(f.MaxPostsPerWeek)
	if err != nil || n < 1 || n > timelineMaxPostsPerWeek {
		return 0, false
	}
	return n, true
}

// keepKnown returns the IDs in ids that known has, each once, in order.
func keepKnown(ids []string, known map[string]bool) []string {
	var kept []string
	for _, id := range ids {
		if known[id] && !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}
	return kept
}

// timelineSummary says what t is made of, for its row on /timelines:
// fragments joined by " · ", such as "Tagged Tech · Mentions “AI” ·
// Leaves out 1 feed". tags are the user's, ordered by name; tag names come
// out in that order, and IDs that aren't among them (deleted since) are
// skipped. A timeline with no conditions at all is "All your feeds".
func timelineSummary(t rss.Timeline, tags []rss.Tag) string {
	f := t.Filter
	var parts []string
	if f.StarredOnly && !f.OnlyIncluded {
		parts = append(parts, "Your stars, newest star first")
	}
	if f.OnlyIncluded {
		names := tagNames(tags, f.IncludeTagIDs)
		if len(names) > 0 {
			parts = append(parts, "Tagged "+joinOr(names))
		}
		if n := len(f.IncludeFeedIDs); n > 0 {
			parts = append(parts, countPhrase(n, "chosen feed", "chosen feeds"))
		}
		if len(names) == 0 && len(f.IncludeFeedIDs) == 0 {
			parts = append(parts, "No feeds chosen")
		}
		if f.StarredOnly {
			parts = append(parts, "Starred only")
		}
	}
	if n := f.MaxPostsPerWeek; n > 0 {
		parts = append(parts, "At most "+countPhrase(n, "post", "posts")+" a week")
	}
	if f.MediaOnly {
		parts = append(parts, "Audio or video")
	}
	if words := wordsPhrase(f.IncludeWords); words != "" {
		parts = append(parts, "Mentions "+words)
	}
	if words := wordsPhrase(f.ExcludeWords); words != "" {
		parts = append(parts, "Leaves out "+words)
	}
	if names := tagNames(tags, f.ExcludeTagIDs); len(names) > 0 {
		parts = append(parts, "Leaves out feeds tagged "+joinOr(names))
	}
	if n := len(f.ExcludeFeedIDs); n > 0 {
		parts = append(parts, "Leaves out "+countPhrase(n, "feed", "feeds"))
	}
	if len(parts) == 0 {
		return "All your feeds"
	}
	return strings.Join(parts, " · ")
}

// tagNames returns the names of the tags among tags whose IDs are in ids,
// in tags' order.
func tagNames(tags []rss.Tag, ids []string) []string {
	var names []string
	for _, tag := range tags {
		if slices.Contains(ids, tag.ID) {
			names = append(names, tag.Name)
		}
	}
	return names
}

// wordsPhrase quotes up to three words or phrases, joined as joinOr does;
// more are counted instead ("5 words or phrases"). "" for none.
func wordsPhrase(words []string) string {
	switch {
	case len(words) == 0:
		return ""
	case len(words) > 3:
		return fmt.Sprintf("%d words or phrases", len(words))
	}
	return joinOr(quoted(words))
}

// joinOr joins items as "A", "A or B", "A, B or C".
func joinOr(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// countPhrase returns "1 {one}" or "{n} {many}".
func countPhrase(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// timelineFormSummary is the box at the top of the timeline form: what the
// timeline shows, in a sentence, and which of the user's feeds it draws
// from. timeline-form.ts rebuilds it as the form changes, with the same rules and
// words as timelineSentence and timelineFeedCount.
type timelineFormSummary struct {
	Sentence string
	Count    string   // "From 4 of your 14 feeds"
	Feeds    []string // the matching feeds' names
	RateHint string   // under "At most N posts a week": how many feeds post that little; "" when N isn't valid
}

// summary returns the form's summary box for the user's tags and subs.
func (f *timelineForm) summary(tags []rss.Tag, subs []rss.Subscription) timelineFormSummary {
	sum := timelineFormSummary{Sentence: timelineSentence(*f, tags, subs)}
	if f.Show == "some" && len(f.IncludeTags) == 0 && len(f.IncludeFeeds) == 0 {
		sum.Count = "No feeds yet"
	} else {
		matched := timelineFeedCount(*f, subs)
		for _, sub := range matched {
			sum.Feeds = append(sum.Feeds, subscriptionName(sub))
		}
		switch {
		case len(matched) == 0:
			sum.Count = "None of your feeds match yet"
		case len(subs) == 1:
			sum.Count = "From your only feed"
		default:
			sum.Count = fmt.Sprintf("From %d of your %d feeds", len(matched), len(subs))
		}
	}
	if n, ok := f.maxPostsPerWeek(); ok && n > 0 {
		quiet := 0
		for _, sub := range subs {
			if sub.Feed.PostsPerWeek <= float64(n) {
				quiet++
			}
		}
		if quiet == 1 {
			sum.RateHint = "1 of your feeds posts this little right now."
		} else {
			sum.RateHint = fmt.Sprintf("%d of your feeds post this little right now.", quiet)
		}
	}
	return sum
}

// subscriptionName is the name the user's reading shows for sub's feed.
func subscriptionName(sub rss.Subscription) string {
	switch {
	case sub.Title != "":
		return sub.Title
	case sub.Feed.Title != "":
		return sub.Feed.Title
	}
	return sub.Feed.Url
}

// timelineSentence says, in one sentence, what the timeline f describes
// shows, such as "Articles mentioning “AI” from feeds tagged Tech, except
// The Verge." It spells out the form's rules (Leave out always wins; every
// ticked condition must hold). Tags come in tags' order (by name) and feeds
// in subs' order. timeline-form.ts builds the same sentence; change both together.
func timelineSentence(f timelineForm, tags []rss.Tag, subs []rss.Subscription) string {
	feedNames := func(ids []string) []string {
		var names []string
		for _, sub := range subs {
			if slices.Contains(ids, sub.Feed.ID) {
				names = append(names, subscriptionName(sub))
			}
		}
		return names
	}
	some := f.Show == "some"
	inTags, inFeeds := tagNames(tags, f.IncludeTags), feedNames(f.IncludeFeeds)
	if some && len(inTags) == 0 && len(inFeeds) == 0 {
		return "Choose at least one tag or feed, or show all your feeds."
	}

	var b strings.Builder
	b.WriteString("Articles")
	if f.StarredOnly {
		b.WriteString(" you’ve starred")
	}
	if f.MediaOnly {
		b.WriteString(" with audio or video")
	}
	var words, notWords []string
	if f.Mentions {
		words = quoted(splitTerms(f.IncludeWords))
	}
	if f.NotMentions {
		notWords = quoted(splitTerms(f.ExcludeWords))
	}
	if len(words) > 0 {
		b.WriteString(" mentioning " + joinOr(words))
	}
	if len(notWords) > 0 {
		if len(words) > 0 {
			b.WriteString(" but not ")
		} else {
			b.WriteString(" not mentioning ")
		}
		b.WriteString(joinOr(notWords))
	}

	rate := ""
	if n, ok := f.maxPostsPerWeek(); f.Quiet && ok && n > 0 {
		rate = "post at most " + strconv.Itoa(n) + " times a week"
		if n == 1 {
			rate = "post at most once a week"
		}
	}
	switch {
	case !some && rate != "":
		b.WriteString(" from feeds that " + rate)
	case !some:
		b.WriteString(" from all your feeds")
	default:
		var src []string
		if len(inTags) > 0 {
			src = append(src, "feeds tagged "+joinOr(inTags))
		}
		src = append(src, inFeeds...)
		b.WriteString(" from " + joinAnd(src))
		switch {
		case rate != "" && len(inFeeds) > 0:
			b.WriteString(", if they " + rate)
		case rate != "":
			b.WriteString(" that " + rate)
		}
	}

	var except []string
	if names := tagNames(tags, f.ExcludeTags); len(names) > 0 {
		except = append(except, "feeds tagged "+joinOr(names))
	}
	except = append(except, feedNames(f.ExcludeFeeds)...)
	if len(except) > 0 {
		b.WriteString(", except " + joinAnd(except))
	}
	b.WriteString(".")
	if f.StarredOnly {
		b.WriteString(" Newest star first.")
	}
	return b.String()
}

// timelineFeedCount returns the subscriptions the timeline f describes draws
// from, in subs' order: those its Show lets through, that Leave out doesn't,
// within its posting rate when Quiet is ticked. Starred, audio and words
// pick articles, not feeds, so they don't count here.
func timelineFeedCount(f timelineForm, subs []rss.Subscription) []rss.Subscription {
	rate := 0
	if n, ok := f.maxPostsPerWeek(); f.Quiet && ok {
		rate = n
	}
	hasTag := func(sub rss.Subscription, ids []string) bool {
		return slices.ContainsFunc(sub.Tags, func(t rss.Tag) bool { return slices.Contains(ids, t.ID) })
	}
	var matched []rss.Subscription
	for _, sub := range subs {
		shown := f.Show != "some" || hasTag(sub, f.IncludeTags) || slices.Contains(f.IncludeFeeds, sub.Feed.ID)
		out := hasTag(sub, f.ExcludeTags) || slices.Contains(f.ExcludeFeeds, sub.Feed.ID)
		if shown && !out && (rate == 0 || sub.Feed.PostsPerWeek <= float64(rate)) {
			matched = append(matched, sub)
		}
	}
	return matched
}

// quoted returns words in curly quotes.
func quoted(words []string) []string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "“" + w + "”"
	}
	return q
}

// joinAnd joins items as "A", "A and B", "A, B and C".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// timeline returns the timeline the form describes; the form must be valid.
// With Show "all" the ticked Show tags and feeds are dropped, and an
// unticked condition stores nothing, whatever its field holds: they'd have
// no effect.
func (f *timelineForm) timeline() rss.Timeline {
	t := rss.Timeline{
		Name:        f.Name,
		Description: f.Description,
		Filter: rss.TimelineFilter{
			ExcludeTagIDs:  f.ExcludeTags,
			ExcludeFeedIDs: f.ExcludeFeeds,
			StarredOnly:    f.StarredOnly,
			MediaOnly:      f.MediaOnly,
		},
	}
	if f.Quiet {
		t.Filter.MaxPostsPerWeek, _ = f.maxPostsPerWeek()
	}
	if f.Mentions {
		t.Filter.IncludeWords = splitTerms(f.IncludeWords)
	}
	if f.NotMentions {
		t.Filter.ExcludeWords = splitTerms(f.ExcludeWords)
	}
	if f.Show == "some" {
		t.Filter.OnlyIncluded = true
		t.Filter.IncludeTagIDs = f.IncludeTags
		t.Filter.IncludeFeedIDs = f.IncludeFeeds
	}
	return t
}

// timelineChecks is the data of one of the timeline form's checkbox lists
// (timeline-tag-checks or timeline-feed-checks).
type timelineChecks struct {
	Field  string // include_tags, include_feeds, exclude_tags or exclude_feeds
	Tags   []rss.Tag
	Subs   []rss.Subscription
	Icons  map[string]string
	Ticked map[string]bool
}

// checks returns the form's four checkbox lists, by field name.
func (f *timelineForm) checks(tags []rss.Tag, subs []rss.Subscription, icons map[string]string) map[string]timelineChecks {
	list := func(field string, ids []string) timelineChecks {
		ticked := map[string]bool{}
		for _, id := range ids {
			ticked[id] = true
		}
		return timelineChecks{Field: field, Tags: tags, Subs: subs, Icons: icons, Ticked: ticked}
	}
	return map[string]timelineChecks{
		"include_tags":  list("include_tags", f.IncludeTags),
		"include_feeds": list("include_feeds", f.IncludeFeeds),
		"exclude_tags":  list("exclude_tags", f.ExcludeTags),
		"exclude_feeds": list("exclude_feeds", f.ExcludeFeeds),
	}
}

// timelineRow is one of the user's custom timelines on /timelines.
type timelineRow struct {
	rss.Timeline
	Summary     string // what it's made of: timelineSummary
	First, Last bool   // its place in tab order, which disables a move button
}

// timelinesHandler lists the user's tabs in order, Home among them, each
// with a summary of what it shows.
func (s *Server) timelinesHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	timelines, err := s.services.TimelineService.ListWithFilters(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	custom := slices.ContainsFunc(timelines, func(t rss.Timeline) bool { return t.ID != "" })
	var tags []rss.Tag
	if custom {
		tags, err = s.services.TagService.List(r.Context(), userID)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	rows := make([]timelineRow, len(timelines))
	for i, t := range timelines {
		summary := "Every feed you follow"
		if t.ID != "" {
			summary = timelineSummary(t, tags)
		}
		rows[i] = timelineRow{Timeline: t, Summary: summary, First: i == 0, Last: i == len(timelines)-1}
	}

	data := struct {
		Nav    navData
		Rows   []timelineRow
		Custom bool // the user has custom timelines, not only Home
		Flash  string
	}{
		Nav:    s.nav(r, "home"),
		Rows:   rows,
		Custom: custom,
		Flash:  s.sessionManager.PopString(r.Context(), "flash"),
	}
	if err := s.renderHTML(w, http.StatusOK, "timelines.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelinePresets are New timeline's starting points: what each ?preset=
// fills in on the form, which shows all feeds unless the preset says.
var timelinePresets = map[string]func(f *timelineForm){
	"tags": func(f *timelineForm) { f.Show = "some" },
	"quiet": func(f *timelineForm) {
		f.Name = "Quiet posters"
		f.Quiet = true
	},
	"starred": func(f *timelineForm) {
		f.Name = "Starred"
		f.StarredOnly = true
	},
	"keywords": func(f *timelineForm) { f.Mentions = true },
	"media": func(f *timelineForm) {
		f.Name = "Audio and video"
		f.MediaOnly = true
	},
	"blank": func(f *timelineForm) {},
}

// timelineNewHandler shows New timeline. Without ?preset= or ?tag= it's a
// list of starting points (timeline_new.html), each a link back here with
// a preset. With a preset in timelinePresets it's the form, filled in as
// the preset says. With ?tag={id} (one of the user's tags, as a tag's page
// links) it's the form named after the tag, showing only its feeds, even
// with a preset. An unknown preset, or a tag that isn't the user's, shows
// the list.
func (s *Server) timelineNewHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	q := r.URL.Query()
	preset, known := timelinePresets[q.Get("preset")]
	if (q.Has("preset") && !known) || (!q.Has("preset") && !q.Has("tag")) {
		s.renderTimelineStart(w, r, userID)
		return
	}

	form := timelineForm{Show: "all", MaxPostsPerWeek: defaultMaxPostsPerWeek}
	if preset != nil {
		preset(&form)
	}
	if q.Has("tag") {
		id := q.Get("tag")
		if !validator.Matches(id, validator.UUIDRX) {
			s.renderTimelineStart(w, r, userID)
			return
		}
		tag, err := s.services.TagService.Get(r.Context(), userID, id)
		switch {
		case errors.Is(err, rss.ErrNoRecord):
			s.renderTimelineStart(w, r, userID)
			return
		case err != nil:
			s.serverErrorHTML(w, r, err)
			return
		}
		form.Name = tag.Name
		form.Show = "some"
		form.IncludeTags = []string{tag.ID}
	}

	s.renderTimelineForm(w, r, http.StatusOK, rss.Timeline{}, form)
}

// renderTimelineStart renders New timeline's list of starting points. The
// user's tags decide only where "Feeds with a tag" goes: to the form, or to
// /tags to make one first.
func (s *Server) renderTimelineStart(w http.ResponseWriter, r *http.Request, userID string) {
	tags, err := s.services.TagService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := struct {
		Nav     navData
		HasTags bool
	}{
		Nav:     s.nav(r, "home"),
		HasTags: len(tags) > 0,
	}
	if err := s.renderHTML(w, http.StatusOK, "timeline_new.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelineCreateHandler makes a timeline and goes to it.
func (s *Server) timelineCreateHandler(w http.ResponseWriter, r *http.Request) {
	form, ok := s.decodeTimelineForm(w, r, rss.Timeline{})
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	id, err := s.services.TimelineService.Create(r.Context(), userID, form.timeline())
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrTooManyTimelines):
			form.AddNonFieldError("You can have up to 20 timelines.")
			s.renderTimelineForm(w, r, http.StatusUnprocessableEntity, rss.Timeline{}, form)
		case errors.Is(err, rss.ErrNoRecord):
			// A tag or feed went away (deleted in another tab) after the form
			// was checked.
			s.notFoundResponse(w, r)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Timeline created.")
	http.Redirect(w, r, "/timelines/"+id, http.StatusSeeOther)
}

// timelineHandler shows a custom timeline: its articles, by the same rules
// as Home.
func (s *Server) timelineHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}
	userID, _ := s.authenticatedUserID(r)
	s.renderTimeline(w, r, userID, t)
}

// timelineEditHandler shows the form to change a timeline.
func (s *Server) timelineEditHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}
	s.renderTimelineForm(w, r, http.StatusOK, t, timelineFormFor(t))
}

// timelineEditPostHandler saves a timeline's name, description and filter.
func (s *Server) timelineEditPostHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}

	form, ok := s.decodeTimelineForm(w, r, t)
	if !ok {
		return
	}

	updated := form.timeline()
	updated.ID = t.ID
	userID, _ := s.authenticatedUserID(r)
	if err := s.services.TimelineService.Update(r.Context(), userID, updated); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Timeline saved.")
	http.Redirect(w, r, "/timelines/"+t.ID, http.StatusSeeOther)
}

// timelineDeleteHandler deletes a timeline. It isn't confirmed first: it's
// on the edit page, and the flash says what was deleted.
func (s *Server) timelineDeleteHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.TimelineService.Delete(r.Context(), userID, t.ID); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", fmt.Sprintf("Deleted the timeline “%s”.", t.Name))
	http.Redirect(w, r, "/timelines", http.StatusSeeOther)
}

// timelineOrderForm is the whole tab order, which reorder.ts posts after a
// drag or a move button press on /timelines.
type timelineOrderForm struct {
	Order []string `form:"order"` // tab IDs in order, "all" for Home
}

// timelineOrderHandler sets the user's tab order. Only reorder.ts posts here, so
// success is a 204. An order that isn't exactly the user's tabs (one made
// or deleted in another window) flashes why and goes back to /timelines,
// where reorder.ts sends the reader.
func (s *Server) timelineOrderHandler(w http.ResponseWriter, r *http.Request) {
	var form timelineOrderForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	ids := make([]string, len(form.Order))
	for i, id := range form.Order {
		switch {
		case id == homeTabID:
			ids[i] = ""
		case validator.Matches(id, validator.UUIDRX):
			ids[i] = id
		default:
			s.formErrorResponse(w, r, fmt.Errorf("order = %q", id))
			return
		}
	}

	userID, _ := s.authenticatedUserID(r)
	err := s.services.TimelineService.Reorder(r.Context(), userID, ids)
	switch {
	case errors.Is(err, rss.ErrOrderChanged):
		s.sessionManager.Put(r.Context(), "flash", "Your timelines changed in another window, so the new order wasn't saved.")
		http.Redirect(w, r, "/timelines", http.StatusSeeOther)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// timelineShareHandler shares a timeline: it gets a share token, unless it
// has one. It needs a confirmed email address.
// timelineMoveForm is the Move up or Move down button on /timelines.
type timelineMoveForm struct {
	Direction string `form:"direction"` // "up" or "down"
}

// timelineMoveHandler moves one of the user's tabs (a custom timeline, or
// Home as "all") one place up or down the tab order, then goes back to its
// row on /timelines.
func (s *Server) timelineMoveHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id != homeTabID && !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	var form timelineMoveForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	if form.Direction != "up" && form.Direction != "down" {
		s.formErrorResponse(w, r, fmt.Errorf("direction = %q", form.Direction))
		return
	}

	userID, _ := s.authenticatedUserID(r)
	storeID := id
	if id == homeTabID {
		storeID = ""
	}
	if err := s.services.TimelineService.Move(r.Context(), userID, storeID, form.Direction == "up"); err != nil {
		s.timelineWriteError(w, r, err)
		return
	}
	http.Redirect(w, r, "/timelines#timeline-"+id, http.StatusSeeOther)
}

func (s *Server) timelineShareHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}
	if !s.emailConfirmed(r) {
		s.redirectToSharing(w, r, t, shareNeedsConfirmation)
		return
	}

	tok, err := token.Random()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	userID, _ := s.authenticatedUserID(r)
	if _, err := s.services.TimelineService.Share(r.Context(), userID, t.ID, tok); err != nil {
		s.timelineWriteError(w, r, err)
		return
	}
	s.redirectToSharing(w, r, t, "Sharing turned on.")
}

// timelineUnshareHandler stops sharing a timeline: its links become 404s.
func (s *Server) timelineUnshareHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.TimelineService.Unshare(r.Context(), userID, t.ID); err != nil {
		s.timelineWriteError(w, r, err)
		return
	}
	s.redirectToSharing(w, r, t, "Sharing turned off. The links no longer work.")
}

// timelineNewLinkHandler replaces a shared timeline's token, so its old
// links stop working. It needs a confirmed email address, like sharing.
func (s *Server) timelineNewLinkHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}
	if !s.emailConfirmed(r) {
		s.redirectToSharing(w, r, t, shareNeedsConfirmation)
		return
	}

	tok, err := token.Random()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	userID, _ := s.authenticatedUserID(r)
	// ErrNoRecord here also means it isn't shared (unshared in another tab):
	// a 404, like a timeline that's gone.
	if err := s.services.TimelineService.NewShareLink(r.Context(), userID, t.ID, tok); err != nil {
		s.timelineWriteError(w, r, err)
		return
	}
	s.redirectToSharing(w, r, t, "New link made. The old one no longer works.")
}

// shareNeedsConfirmation is the flash for sharing with an unconfirmed email
// address.
const shareNeedsConfirmation = "Confirm your email address to share timelines."

// redirectToSharing flashes msg and goes to t's share page.
func (s *Server) redirectToSharing(w http.ResponseWriter, r *http.Request, t rss.Timeline, msg string) {
	s.sessionManager.Put(r.Context(), "flash", msg)
	http.Redirect(w, r, "/timelines/"+t.ID+"/share", http.StatusSeeOther)
}

// timelineSharePageHandler shows a timeline's share page: sharing it, its
// links, a new link and stopping, or confirming the email address first.
func (s *Server) timelineSharePageHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.timelineFromURL(w, r)
	if !ok {
		return
	}
	sharing := timelineSharing{
		Confirmed: s.emailConfirmed(r),
		ReturnTo:  "/timelines/" + t.ID + "/share",
	}
	if t.ShareToken != "" {
		sharing.PageURL, sharing.FeedURL = s.sharedURLs(t.ShareToken)
	}
	data := struct {
		Nav      navData
		Timeline rss.Timeline
		Sharing  timelineSharing
		Flash    string
	}{
		Nav:      s.nav(r, "home"),
		Timeline: t,
		Sharing:  sharing,
		Flash:    s.sessionManager.PopString(r.Context(), "flash"),
	}
	if err := s.renderHTML(w, http.StatusOK, "timeline_share.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelineWriteError writes the response for err from changing a timeline:
// 404 for rss.ErrNoRecord, else 500.
func (s *Server) timelineWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, rss.ErrNoRecord) {
		s.notFoundResponse(w, r)
	} else {
		s.serverErrorHTML(w, r, err)
	}
}

// emailConfirmed reports whether the logged-in user has confirmed their
// email address.
func (s *Server) emailConfirmed(r *http.Request) bool {
	sess, ok := s.authenticatedSession(r)
	return ok && !sess.User.EmailConfirmedAt.IsZero()
}

// timelineSharing is what a timeline's share page shows.
type timelineSharing struct {
	Confirmed bool
	// PageURL and FeedURL are the shared links; "" unless it's shared.
	PageURL  string
	FeedURL  string
	ReturnTo string // for the confirmation notice's Resend
}

// timelineFromURL returns the user's custom timeline named in the URL. If ok
// is false it has already written the response: 404 if the id isn't a UUID,
// or the user has no such timeline.
func (s *Server) timelineFromURL(w http.ResponseWriter, r *http.Request) (t rss.Timeline, ok bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.Timeline{}, false
	}

	userID, _ := s.authenticatedUserID(r)
	t, err := s.services.TimelineService.Get(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Timeline{}, false
	}
	return t, true
}

// decodeTimelineForm decodes and checks the timeline form posted for t (the
// zero Timeline when creating one). If ok is false it has already written
// the response: 400 for an unreadable form, else the form again with 422.
func (s *Server) decodeTimelineForm(w http.ResponseWriter, r *http.Request, t rss.Timeline) (form timelineForm, ok bool) {
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return form, false
	}
	// The form only sends these.
	if form.Show != "all" && form.Show != "some" {
		s.formErrorResponse(w, r, fmt.Errorf("show = %q", form.Show))
		return form, false
	}

	tags, subs, ok := s.timelineSources(w, r)
	if !ok {
		return form, false
	}
	form.check(tags, subs)
	if !form.Valid() {
		s.renderTimelineFormWith(w, r, http.StatusUnprocessableEntity, t, form, tags, subs)
		return form, false
	}
	return form, true
}

// timelineSources returns the user's tags and subscriptions, which the
// timeline form offers. If ok is false it has already written the response.
func (s *Server) timelineSources(w http.ResponseWriter, r *http.Request) (tags []rss.Tag, subs []rss.Subscription, ok bool) {
	userID, _ := s.authenticatedUserID(r)
	tags, err := s.services.TagService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return nil, nil, false
	}
	subs, err = s.services.SubscriptionService.ListByUser(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return nil, nil, false
	}
	return tags, subs, true
}

// renderTimelineForm renders timeline_form.html with status and form, for
// t (the zero Timeline for New timeline).
func (s *Server) renderTimelineForm(w http.ResponseWriter, r *http.Request, status int, t rss.Timeline, form timelineForm) {
	tags, subs, ok := s.timelineSources(w, r)
	if !ok {
		return
	}
	s.renderTimelineFormWith(w, r, status, t, form, tags, subs)
}

// renderTimelineFormWith is renderTimelineForm with the user's tags and
// subscriptions already read.
func (s *Server) renderTimelineFormWith(w http.ResponseWriter, r *http.Request, status int, t rss.Timeline, form timelineForm, tags []rss.Tag, subs []rss.Subscription) {
	siteIDs := make([]string, len(subs))
	for i, sub := range subs {
		siteIDs[i] = sub.Site.ID
	}

	data := struct {
		Nav      navData
		Timeline rss.Timeline
		Form     timelineForm
		Tags     []rss.Tag
		Subs     []rss.Subscription
		Checks   map[string]timelineChecks
		Summary  timelineFormSummary
		LeaveOut int // how many tags and feeds are left out, for the disclosure's label
		Flash    string
	}{
		Nav:      s.nav(r, "home"),
		Timeline: t,
		Form:     form,
		Tags:     tags,
		Subs:     subs,
		Checks:   form.checks(tags, subs, s.pageIcons(r, siteIDs)),
		Summary:  form.summary(tags, subs),
		LeaveOut: len(form.ExcludeTags) + len(form.ExcludeFeeds),
	}
	if t.ID != "" {
		data.Flash = s.sessionManager.PopString(r.Context(), "flash")
	}
	if err := s.renderHTML(w, status, "timeline_form.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
