package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// tagNameMaxChars is the longest tag name.
const tagNameMaxChars = 50

type tagForm struct {
	Name                string `form:"name"`
	validator.Validator `form:"-"`
}

// check normalizes the name with rss.TagName and validates it.
func (f *tagForm) check() {
	f.Name = rss.TagName(f.Name)
	f.CheckField(validator.NotBlank(f.Name), "name", "Enter a name.")
	f.CheckField(validator.MaxChars(f.Name, tagNameMaxChars), "name", "Must be 50 characters or fewer.")
	// The Edit subscription form separates new tags with commas.
	f.CheckField(!strings.Contains(f.Name, ","), "name", "Tags can't contain commas.")
}

// addStoreError adds the name's error for rss.ErrDuplicateTag and
// rss.ErrTooManyTags, and reports whether err was one of them.
func (f *tagForm) addStoreError(err error) bool {
	switch {
	case errors.Is(err, rss.ErrDuplicateTag):
		f.AddFieldError("name", fmt.Sprintf("You already have a tag called “%s”.", f.Name))
	case errors.Is(err, rss.ErrTooManyTags):
		f.AddFieldError("name", "You can have up to 100 tags.")
	default:
		return false
	}
	return true
}

type tagFeedsForm struct {
	Feeds []string `form:"feeds"`
}

// tagsHandler lists the user's tags, with a form to make a new one.
func (s *Server) tagsHandler(w http.ResponseWriter, r *http.Request) {
	s.renderTags(w, r, http.StatusOK, tagForm{})
}

// tagCreateHandler makes a tag and goes to its page, where its feeds can be
// ticked.
func (s *Server) tagCreateHandler(w http.ResponseWriter, r *http.Request) {
	var form tagForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.check()
	if !form.Valid() {
		s.renderTags(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	tag, err := s.services.TagService.Create(r.Context(), userID, form.Name)
	if err != nil {
		if form.addStoreError(err) {
			s.renderTags(w, r, http.StatusUnprocessableEntity, form)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	http.Redirect(w, r, "/tags/"+tag.ID, http.StatusSeeOther)
}

// tagHandler shows a tag: a checklist of the user's subscriptions, ticked
// where they have the tag, and forms to rename and delete it.
func (s *Server) tagHandler(w http.ResponseWriter, r *http.Request) {
	tag, ok := s.tagFromURL(w, r)
	if !ok {
		return
	}
	s.renderTag(w, r, http.StatusOK, tag, tagForm{Name: tag.Name})
}

// tagFeedsHandler makes the ticked subscriptions exactly the ones with the
// tag. Feeds the user doesn't subscribe to (say, unsubscribed in another
// tab since the page loaded) are ignored.
func (s *Server) tagFeedsHandler(w http.ResponseWriter, r *http.Request) {
	tag, ok := s.tagFromURL(w, r)
	if !ok {
		return
	}

	var form tagFeedsForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	// Postgres rejects a malformed UUID, so drop those before asking.
	var ids []string
	for _, id := range form.Feeds {
		if validator.Matches(id, validator.UUIDRX) {
			ids = append(ids, id)
		}
	}
	userID, _ := s.authenticatedUserID(r)
	subscribed, err := s.services.SubscriptionService.SubscribedFeedIDs(r.Context(), userID, ids)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	feedIDs := []string{}
	for _, id := range ids {
		if subscribed[id] {
			feedIDs = append(feedIDs, id)
		}
	}

	if err := s.services.TagService.SetFeeds(r.Context(), userID, tag.ID, feedIDs); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", fmt.Sprintf("Saved the feeds tagged “%s”.", tag.Name))
	http.Redirect(w, r, "/tags/"+tag.ID, http.StatusSeeOther)
}

// tagRenameHandler renames a tag.
func (s *Server) tagRenameHandler(w http.ResponseWriter, r *http.Request) {
	tag, ok := s.tagFromURL(w, r)
	if !ok {
		return
	}

	var form tagForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.check()
	if !form.Valid() {
		s.renderTag(w, r, http.StatusUnprocessableEntity, tag, form)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.TagService.Rename(r.Context(), userID, tag.ID, form.Name); err != nil {
		switch {
		case errors.Is(err, rss.ErrNoRecord):
			s.notFoundResponse(w, r)
		case form.addStoreError(err):
			s.renderTag(w, r, http.StatusUnprocessableEntity, tag, form)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Tag renamed.")
	http.Redirect(w, r, "/tags/"+tag.ID, http.StatusSeeOther)
}

// tagDeleteHandler deletes a tag, which removes it from its subscriptions
// and timelines. Like unsubscribing, it isn't confirmed first.
func (s *Server) tagDeleteHandler(w http.ResponseWriter, r *http.Request) {
	tag, ok := s.tagFromURL(w, r)
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.TagService.Delete(r.Context(), userID, tag.ID); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", fmt.Sprintf("Deleted the tag “%s”.", tag.Name))
	http.Redirect(w, r, "/tags", http.StatusSeeOther)
}

// tagFromURL returns the user's tag named in the URL. If ok is false it has
// already written the response: 404 if the id isn't a UUID or the user has no
// such tag.
func (s *Server) tagFromURL(w http.ResponseWriter, r *http.Request) (tag rss.Tag, ok bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.Tag{}, false
	}

	userID, _ := s.authenticatedUserID(r)
	tag, err := s.services.TagService.Get(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Tag{}, false
	}
	return tag, true
}

// renderTags renders tags.html with status and form as the New tag form.
func (s *Server) renderTags(w http.ResponseWriter, r *http.Request, status int, form tagForm) {
	userID, _ := s.authenticatedUserID(r)
	tags, err := s.services.TagService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Nav   navData
		Tags  []rss.Tag
		Form  tagForm
		Flash string
	}{
		Nav:   s.nav(r, "feeds"),
		Tags:  tags,
		Form:  form,
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}
	if err := s.renderHTML(w, status, "tags.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// renderTag renders tag.html for tag with status and form as the Rename form.
func (s *Server) renderTag(w http.ResponseWriter, r *http.Request, status int, tag rss.Tag, form tagForm) {
	userID, _ := s.authenticatedUserID(r)
	subs, err := s.services.SubscriptionService.ListByUser(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	tagged, err := s.services.TagService.FeedIDs(r.Context(), userID, tag.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	siteIDs := make([]string, len(subs))
	for i, sub := range subs {
		siteIDs[i] = sub.Site.ID
	}

	data := struct {
		Nav    navData
		Tag    rss.Tag
		Subs   []rss.Subscription
		Tagged map[string]bool
		Icons  map[string]string
		Form   tagForm
		Flash  string
	}{
		Nav:    s.nav(r, "feeds"),
		Tag:    tag,
		Subs:   subs,
		Tagged: tagged,
		Icons:  s.pageIcons(r, siteIDs),
		Form:   form,
		Flash:  s.sessionManager.PopString(r.Context(), "flash"),
	}
	if err := s.renderHTML(w, status, "tag.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
