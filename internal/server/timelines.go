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
	MaxPostsPerWeek     string   `form:"max_posts_per_week"` // "": any rate
	MediaOnly           bool     `form:"media_only"`
	IncludeWords        string   `form:"include_words"` // comma-separated
	ExcludeWords        string   `form:"exclude_words"`
	validator.Validator `form:"-"`
}

// timelineFormFor returns the form showing t's settings.
func timelineFormFor(t rss.Timeline) timelineForm {
	form := timelineForm{
		Name:         t.Name,
		Description:  t.Description,
		Show:         "all",
		ExcludeTags:  t.Filter.ExcludeTagIDs,
		ExcludeFeeds: t.Filter.ExcludeFeedIDs,
		StarredOnly:  t.Filter.StarredOnly,
		MediaOnly:    t.Filter.MediaOnly,
		IncludeWords: strings.Join(t.Filter.IncludeWords, ", "),
		ExcludeWords: strings.Join(t.Filter.ExcludeWords, ", "),
	}
	if t.Filter.MaxPostsPerWeek > 0 {
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
// or "some".
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
	_, ok := f.maxPostsPerWeek()
	f.CheckField(ok, "max_posts_per_week", "Enter a whole number from 1 to 100, or leave it blank.")
	f.checkTerms(splitTerms(f.IncludeWords), "include_words")
	f.checkTerms(splitTerms(f.ExcludeWords), "exclude_words")
}

// checkTerms validates the words or phrases of field.
func (f *timelineForm) checkTerms(terms []string, field string) {
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

// timeline returns the timeline the form describes; the form must be valid.
// With Show "all" the ticked Show tags and feeds are dropped: they'd have no
// effect.
func (f *timelineForm) timeline() rss.Timeline {
	maxPostsPerWeek, _ := f.maxPostsPerWeek()
	t := rss.Timeline{
		Name:        f.Name,
		Description: f.Description,
		Filter: rss.TimelineFilter{
			ExcludeTagIDs:   f.ExcludeTags,
			ExcludeFeedIDs:  f.ExcludeFeeds,
			StarredOnly:     f.StarredOnly,
			MaxPostsPerWeek: maxPostsPerWeek,
			MediaOnly:       f.MediaOnly,
			IncludeWords:    splitTerms(f.IncludeWords),
			ExcludeWords:    splitTerms(f.ExcludeWords),
		},
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

// timelinesHandler lists the user's custom timelines.
func (s *Server) timelinesHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	timelines, err := s.services.TimelineService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Nav       navData
		Timelines []rss.Timeline
		Flash     string
	}{
		Nav:       s.nav(r, "home"),
		Timelines: timelines,
		Flash:     s.sessionManager.PopString(r.Context(), "flash"),
	}
	if err := s.renderHTML(w, http.StatusOK, "timelines.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelineNewHandler shows the New timeline form, showing all feeds. With
// ?preset=starred it's named Starred and shows only starred articles; with
// ?preset=quiet it's named Quiet posters and shows only feeds posting at
// most 2 times a week. With
// ?tag={id} (one of the user's tags) it's named after the tag (even with a
// preset) and shows only its feeds. Any other tag or preset is ignored.
func (s *Server) timelineNewHandler(w http.ResponseWriter, r *http.Request) {
	form := timelineForm{Show: "all"}

	switch r.URL.Query().Get("preset") {
	case "starred":
		form.Name = "Starred"
		form.StarredOnly = true
	case "quiet":
		form.Name = "Quiet posters"
		form.MaxPostsPerWeek = "2"
	}

	if id := r.URL.Query().Get("tag"); validator.Matches(id, validator.UUIDRX) {
		userID, _ := s.authenticatedUserID(r)
		tag, err := s.services.TagService.Get(r.Context(), userID, id)
		switch {
		case err == nil:
			form.Name = tag.Name
			form.Show = "some"
			form.IncludeTags = []string{tag.ID}
		case !errors.Is(err, rss.ErrNoRecord):
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	s.renderTimelineForm(w, r, http.StatusOK, rss.Timeline{}, form)
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

// timelineShareHandler shares a timeline: it gets a share token, unless it
// has one. It needs a confirmed email address.
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

// redirectToSharing flashes msg and goes to the Sharing section of t's edit
// page.
func (s *Server) redirectToSharing(w http.ResponseWriter, r *http.Request, t rss.Timeline, msg string) {
	s.sessionManager.Put(r.Context(), "flash", msg)
	http.Redirect(w, r, "/timelines/"+t.ID+"/edit#sharing", http.StatusSeeOther)
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

// timelineSharing is the Sharing section of a timeline's edit page.
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
		Checks   map[string]timelineChecks
		Sharing  *timelineSharing // nil for New timeline
		Flash    string
	}{
		Nav:      s.nav(r, "home"),
		Timeline: t,
		Form:     form,
		Tags:     tags,
		Checks:   form.checks(tags, subs, s.pageIcons(r, siteIDs)),
	}
	if t.ID != "" {
		data.Sharing = &timelineSharing{
			Confirmed: s.emailConfirmed(r),
			ReturnTo:  "/timelines/" + t.ID + "/edit#sharing",
		}
		if t.ShareToken != "" {
			data.Sharing.PageURL, data.Sharing.FeedURL = s.sharedURLs(t.ShareToken)
		}
		data.Flash = s.sessionManager.PopString(r.Context(), "flash")
	}
	if err := s.renderHTML(w, status, "timeline_form.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
