package server

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) settingsHandler(w http.ResponseWriter, r *http.Request) {
	nav := s.nav(r, "settings")
	data := struct {
		Nav           navData
		User          rss.User
		Flash         string
		ConfirmNotice *confirmNotice
		SupportEmail  string
		DebugAllowed  bool
		DebugOn       bool
		Proto, Tab    string
		Future        bool
	}{
		Proto:         r.URL.Query().Get("proto"),
		Tab:           r.URL.Query().Get("tab"),
		Future:        r.URL.Query().Get("future") != "",
		Nav:           nav,
		User:          nav.User,
		Flash:         s.sessionManager.PopString(r.Context(), "flash"),
		ConfirmNotice: s.confirmNotice(r),
		SupportEmail:  s.config.SupportEmail,
		DebugAllowed:  s.debugAllowed(r),
		DebugOn:       nav.Debug,
	}

	page := "settings.html"
	if data.Proto != "" {
		page = "settings_proto_" + data.Proto + ".html"
	}
	if err := s.renderHTML(w, http.StatusOK, page, data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type nameForm struct {
	Name                string `form:"name"`
	validator.Validator `form:"-"`
}

type settingsNamePage struct {
	Nav  navData
	Form nameForm
}

// renderSettingsName renders settings_name.html with status.
func (s *Server) renderSettingsName(w http.ResponseWriter, r *http.Request, status int, form nameForm) {
	data := settingsNamePage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_name.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsNameHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticatedSession(r)
	s.renderSettingsName(w, r, http.StatusOK, nameForm{Name: sess.User.Name})
}

func (s *Server) settingsNameFormHandler(w http.ResponseWriter, r *http.Request) {
	var form nameForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.Name = strings.TrimSpace(form.Name)
	form.CheckName(form.Name)
	if !form.Valid() {
		s.renderSettingsName(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.UserService.UpdateName(r.Context(), userID, form.Name); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Your name was updated.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type themeForm struct {
	Theme               string `form:"theme"`
	validator.Validator `form:"-"`
}

type settingsThemePage struct {
	Nav  navData
	Form themeForm
}

// renderSettingsTheme renders settings_theme.html with status.
func (s *Server) renderSettingsTheme(w http.ResponseWriter, r *http.Request, status int, form themeForm) {
	data := settingsThemePage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_theme.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsThemeHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticatedSession(r)
	s.renderSettingsTheme(w, r, http.StatusOK, themeForm{Theme: sess.User.Theme})
}

func (s *Server) settingsThemeFormHandler(w http.ResponseWriter, r *http.Request) {
	var form themeForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.CheckField(slices.Contains([]string{rss.ThemeSystem, rss.ThemeLight, rss.ThemeDark}, form.Theme), "theme", "Choose System, Light or Dark.")
	if !form.Valid() {
		s.renderSettingsTheme(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.UserService.UpdateTheme(r.Context(), userID, form.Theme); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Your theme was updated.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type passwordForm struct {
	CurrentPassword     string `form:"current_password"`
	NewPassword         string `form:"new_password"`
	validator.Validator `form:"-"`
}

type settingsPasswordPage struct {
	Nav  navData
	Form passwordForm
}

// renderSettingsPassword renders settings_password.html with status. It never
// echoes the passwords back.
func (s *Server) renderSettingsPassword(w http.ResponseWriter, r *http.Request, status int, form passwordForm) {
	form.CurrentPassword, form.NewPassword = "", ""
	data := settingsPasswordPage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_password.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsPasswordHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsPassword(w, r, http.StatusOK, passwordForm{})
}

func (s *Server) settingsPasswordFormHandler(w http.ResponseWriter, r *http.Request) {
	var form passwordForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	sess, _ := s.authenticatedSession(r)
	user := sess.User

	if ok, retryAfter := s.passwordChangeLimiter.AllowWithRetry(user.ID); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderSettingsPassword(w, r, http.StatusTooManyRequests, form)
		return
	}

	form.CheckField(validator.NotBlank(form.CurrentPassword), "current_password", "Enter your current password.")
	form.CheckNewPassword("new_password", form.NewPassword, user.Email)
	form.CheckField(form.NewPassword != form.CurrentPassword, "new_password", "Your new password must be different from your current one.")
	if !form.Valid() {
		s.renderSettingsPassword(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	err := s.services.UserService.ChangePassword(r.Context(), user.ID, sess.ID, form.CurrentPassword, form.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrWrongPassword):
			form.AddFieldError("current_password", "That password isn't right.")
			s.renderSettingsPassword(w, r, http.StatusUnprocessableEntity, form)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// The password has changed either way, so a failure to queue the notice
	// doesn't fail the page.
	if err := s.sendPasswordChanged(r.Context(), user); err != nil {
		s.logError(r, err)
	}

	s.sessionManager.Put(r.Context(), "flash", "Your password was changed. Other devices were logged out.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) logOutOthersHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticatedSession(r)
	n, err := s.services.SessionService.DeleteOthers(r.Context(), sess.User.ID, sess.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	flash := "You're logged out on every other device."
	if n == 0 {
		flash = "There were no other devices to log out."
	}
	s.sessionManager.Put(r.Context(), "flash", flash)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type deleteAccountForm struct {
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

type settingsDeletePage struct {
	Nav  navData
	Form deleteAccountForm
}

// renderSettingsDelete renders settings_delete.html with status. It never
// echoes the password back.
func (s *Server) renderSettingsDelete(w http.ResponseWriter, r *http.Request, status int, form deleteAccountForm) {
	form.Password = ""
	data := settingsDeletePage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_delete.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsDeleteHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsDelete(w, r, http.StatusOK, deleteAccountForm{})
}

func (s *Server) settingsDeleteFormHandler(w http.ResponseWriter, r *http.Request) {
	var form deleteAccountForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	userID, _ := s.authenticatedUserID(r)

	if ok, retryAfter := s.deleteAccountLimiter.AllowWithRetry(userID); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderSettingsDelete(w, r, http.StatusTooManyRequests, form)
		return
	}

	form.CheckField(validator.NotBlank(form.Password), "password", "Enter your password.")
	if !form.Valid() {
		s.renderSettingsDelete(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	old, err := s.services.UserService.Delete(r.Context(), userID, form.Password)
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrWrongPassword):
			form.AddFieldError("password", "That password isn't right.")
			s.renderSettingsDelete(w, r, http.StatusUnprocessableEntity, form)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// The account is gone either way, so a failure to queue the notice
	// doesn't fail the page. The outbox has no foreign key to users, so the
	// email outlives the account.
	if err := s.sendEmail(r.Context(), old.Email, "account_deleted", struct{ Name string }{old.Name}); err != nil {
		s.logError(r, err)
	}

	// The user_sessions row went with the account; this forgets it in the
	// browser too.
	if err := s.sessionManager.RenewToken(r.Context()); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.sessionManager.Remove(r.Context(), "sessionID")
	s.sessionManager.Put(r.Context(), "flash", "Your account was deleted. Thanks for trying Paper Round.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) changePasswordWellKnownHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/password", http.StatusFound)
}

// settingsExportHandler shows the export's options; its form downloads
// the file from exportOPMLHandler.
func (s *Server) settingsExportHandler(w http.ResponseWriter, r *http.Request) {
	data := struct{ Nav navData }{Nav: s.nav(r, "settings")}
	if err := s.renderHTML(w, http.StatusOK, "settings_export.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// opmlDoc is an OPML 2.0 document of the user's subscriptions. Readers turn
// nested outlines into folders, so feeds are nested in a folder per tag, or
// listed flat.
type opmlDoc struct {
	XMLName xml.Name      `xml:"opml"`
	Version string        `xml:"version,attr"`
	Head    opmlHead      `xml:"head"`
	Body    []opmlOutline `xml:"body>outline"`
}

type opmlHead struct {
	Title       string `xml:"title"`
	DateCreated string `xml:"dateCreated"` // time.RFC1123Z
}

// opmlOutline is a feed, or a folder (a tag) of feeds.
type opmlOutline struct {
	Type     string        `xml:"type,attr,omitempty"`    // "rss"; "" for a folder
	Text     string        `xml:"text,attr"`              // the user's name for the feed, else its title, else its URL; the tag's name
	Title    string        `xml:"title,attr,omitempty"`   // the user's name for the feed, else its title; the tag's name
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`  // Feed.Url; "" for a folder
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"` // Feed.SiteUrl, else Site.URL
	Outlines []opmlOutline `xml:"outline,omitempty"`      // a folder's feeds
}

// exportOPMLHandler downloads the user's subscriptions, gone feeds included,
// as an OPML file: with ?tags=ignore a flat list of feeds, else a folder per
// tag, ordered by name, then the untagged feeds.
func (s *Server) exportOPMLHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	subs, err := s.services.SubscriptionService.ListByUser(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	now := time.Now().UTC()
	doc := opmlDoc{
		Version: "2.0",
		Head:    opmlHead{Title: "Paper Round subscriptions", DateCreated: now.Format(time.RFC1123Z)},
	}
	if r.URL.Query().Get("tags") == "ignore" {
		for _, sub := range subs {
			doc.Body = append(doc.Body, feedOutline(sub))
		}
	} else {
		doc.Body = tagFolders(subs)
	}

	// Encoded into a buffer first, so a failure never sends half a file.
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	buf.WriteByte('\n')

	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="paper-round-subscriptions-`+now.Format(time.DateOnly)+`.opml"`)
	if _, err := w.Write(buf.Bytes()); err != nil {
		s.logError(r, err)
	}
}

// feedOutline is sub's feed as an OPML outline.
func feedOutline(sub rss.Subscription) opmlOutline {
	return opmlOutline{
		Type:    "rss",
		Text:    cmp.Or(sub.Title, sub.Feed.Title, sub.Feed.Url),
		Title:   cmp.Or(sub.Title, sub.Feed.Title),
		XMLURL:  sub.Feed.Url,
		HTMLURL: cmp.Or(sub.Feed.SiteUrl, sub.Site.URL),
	}
}

// tagFolders is subs as OPML outlines: a folder per tag, ordered by name
// ignoring case, with its feeds in subs' order (a feed with two tags is in
// both), then the untagged feeds.
func tagFolders(subs []rss.Subscription) []opmlOutline {
	var tags []rss.Tag
	feeds := map[string][]opmlOutline{} // by tag ID
	var untagged []opmlOutline
	for _, sub := range subs {
		o := feedOutline(sub)
		if len(sub.Tags) == 0 {
			untagged = append(untagged, o)
		}
		for _, tag := range sub.Tags {
			if _, ok := feeds[tag.ID]; !ok {
				tags = append(tags, tag)
			}
			feeds[tag.ID] = append(feeds[tag.ID], o)
		}
	}
	slices.SortFunc(tags, func(a, b rss.Tag) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	var outlines []opmlOutline
	for _, tag := range tags {
		outlines = append(outlines, opmlOutline{Text: tag.Name, Title: tag.Name, Outlines: feeds[tag.ID]})
	}
	return append(outlines, untagged...)
}
