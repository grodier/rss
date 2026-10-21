package server

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net/http"
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
	}{
		Nav:           nav,
		User:          nav.User,
		Flash:         s.sessionManager.PopString(r.Context(), "flash"),
		ConfirmNotice: s.confirmNotice(r),
		SupportEmail:  s.config.SupportEmail,
	}

	if err := s.renderHTML(w, http.StatusOK, "settings.html", data); err != nil {
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
	if err := s.sendEmail(r.Context(), user.Email, "password_changed", struct{ Name string }{user.Name}); err != nil {
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
	s.sessionManager.Put(r.Context(), "flash", "Your account was deleted. Thanks for trying rss.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) changePasswordWellKnownHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/password", http.StatusFound)
}

// opmlDoc is an OPML 2.0 document of the user's subscriptions: a flat list,
// one outline per feed, since readers turn nested outlines into folders.
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

type opmlOutline struct {
	Type    string `xml:"type,attr"`              // "rss"
	Text    string `xml:"text,attr"`              // feed title, else its URL
	Title   string `xml:"title,attr,omitempty"`   // feed title
	XMLURL  string `xml:"xmlUrl,attr"`            // Feed.Url
	HTMLURL string `xml:"htmlUrl,attr,omitempty"` // Feed.SiteUrl, else Site.URL
}

// exportOPMLHandler downloads the user's subscriptions, gone feeds included,
// as an OPML file.
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
		Head:    opmlHead{Title: "rss subscriptions", DateCreated: now.Format(time.RFC1123Z)},
	}
	for _, sub := range subs {
		o := opmlOutline{
			Type:    "rss",
			Text:    sub.Feed.Title,
			Title:   sub.Feed.Title,
			XMLURL:  sub.Feed.Url,
			HTMLURL: sub.Feed.SiteUrl,
		}
		if o.Text == "" {
			o.Text = sub.Feed.Url
		}
		if o.HTMLURL == "" {
			o.HTMLURL = sub.Site.URL
		}
		doc.Body = append(doc.Body, o)
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
	w.Header().Set("Content-Disposition", `attachment; filename="rss-subscriptions-`+now.Format(time.DateOnly)+`.opml"`)
	if _, err := w.Write(buf.Bytes()); err != nil {
		s.logError(r, err)
	}
}
