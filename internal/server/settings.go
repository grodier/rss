package server

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) settingsHandler(w http.ResponseWriter, r *http.Request) {
	nav := s.nav(r, "settings")
	data := struct {
		Nav   navData
		User  rss.User
		Flash string
	}{
		Nav:   nav,
		User:  nav.User,
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
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
