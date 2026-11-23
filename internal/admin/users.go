package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// Users and support: finding a reader's account and seeing what state it's
// in, read-only. Aggregates only: a reader's page shows account facts and
// counts, never their reads, saves, stars, tags, timelines or feeds, and
// no one is impersonated ("Admin" in docs/decisions.md).

// usersPageSize is how many users the users page lists at once.
const usersPageSize = 50

// usersParams are the users page's URL parameters: the search and the
// user whose row the page starts after.
type usersParams struct {
	q, after string
}

// href is the users page's URL with p, leaving out what's empty.
func (p usersParams) href() string {
	v := url.Values{}
	if p.q != "" {
		v.Set("q", p.q)
	}
	if p.after != "" {
		v.Set("after", p.after)
	}
	if len(v) == 0 {
		return "/users"
	}
	return "/users?" + v.Encode()
}

// usersPage is the users page's data.
type usersPage struct {
	Nav  navData
	Q    string
	Rows []rss.AdminUserRow
	Now  time.Time
	// Next is the URL of the next page, "" on the last.
	Next string
}

// usersHandler lists readers, newest first, usersPageSize at a time,
// those whose email or name starts with ?q= (any case), from after the
// user ?after=.
func (s *Server) usersHandler(w http.ResponseWriter, r *http.Request) {
	params := usersParams{q: strings.TrimSpace(r.URL.Query().Get("q")), after: r.URL.Query().Get("after")}
	if params.after != "" && !validator.Matches(params.after, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}
	// One more than a page, to know whether there's another.
	rows, err := s.services.Users.ListUsers(r.Context(), rss.AdminUserQuery{Q: params.q, After: params.after, Limit: usersPageSize + 1})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if params.after != "" && len(rows) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	page := usersPage{Nav: s.nav(r, "users"), Q: params.q, Now: s.now()}
	if len(rows) > usersPageSize {
		rows = rows[:usersPageSize]
		next := params
		next.after = rows[len(rows)-1].ID
		page.Next = next.href()
	}
	page.Rows = rows

	if err := s.renderHTML(w, http.StatusOK, "users.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// userPage is a reader's page's data. It holds account facts and counts
// only: TestUserPageHoldsNoReading keeps it that way.
type userPage struct {
	Nav  navData
	User rss.AdminUser
	// Counts are the reader's counts as a stat row.
	Counts []statTile
	// Emails are the outbox's emails to the reader's address, and Tokens
	// their outstanding email links, as the page says them.
	Emails []userEmail
	Tokens []userToken
	// History is the reader's entries in the audit log.
	History string
	Now     time.Time
}

// userEmail is an email in the outbox as a reader's page shows it: its
// status in words, in the danger tone when it failed.
type userEmail struct {
	rss.AdminUserEmail
	Status string
	Tone   string
}

// userToken is an outstanding email link as a reader's page shows it: its
// purpose in words.
type userToken struct {
	rss.AdminUserToken
	Purpose string
}

// tokenPurposes say what each kind of email link is for.
var tokenPurposes = map[string]string{
	rss.TokenConfirmEmail:  "Confirm email",
	rss.TokenResetPassword: "Reset password",
	rss.TokenChangeEmail:   "Change email",
}

// emailStatuses say an outbox email's status, and its tone, a status
// label's class suffix: an invite's tones for waiting and done
// (status-pending, status-redeemed), and the danger tone when the outbox
// gave up.
var emailStatuses = map[string]struct{ Text, Tone string }{
	"pending": {"Pending", "pending"},
	"sent":    {"Sent", "redeemed"},
	"failed":  {"Failed", "danger"},
}

// userCounts are a reader's counts as a stat row: counts only, never what
// they count.
func userCounts(c rss.AdminUserCounts) []statTile {
	timelines := statTile{Label: "Timelines", Value: c.Timelines, Note: "none shared"}
	if c.SharedTimelines > 0 {
		timelines.Note = count(c.SharedTimelines) + " shared"
	}
	imports := statTile{Label: "Imports", Value: c.Imports, Note: "none yet"}
	if c.Imports > 0 {
		imports.Note = fmt.Sprintf("%s feeds subscribed, %s failed", count(c.ImportedFeeds), count(c.ImportFailures))
	}
	return []statTile{
		{Label: "Devices", Value: c.Devices, Note: "logged in"},
		{Label: "Subscriptions", Value: c.Subscriptions},
		{Label: "Tags", Value: c.Tags},
		timelines,
		{Label: "Saved", Value: c.Saved, Note: "articles"},
		{Label: "Starred", Value: c.Starred, Note: "articles"},
		imports,
	}
}

// userHandler shows the reader {id}'s account, counts and emails.
func (s *Server) userHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}
	user, err := s.services.Users.User(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}

	page := userPage{
		Nav:     s.nav(r, "users"),
		User:    user,
		Counts:  userCounts(user.Counts),
		History: auditHistoryURL("user", user.ID),
		Now:     s.now(),
	}
	for _, e := range user.Emails {
		st, ok := emailStatuses[e.Status]
		if !ok {
			st.Text, st.Tone = e.Status, "pending"
		}
		page.Emails = append(page.Emails, userEmail{AdminUserEmail: e, Status: st.Text, Tone: st.Tone})
	}
	for _, t := range user.Tokens {
		purpose, ok := tokenPurposes[t.Purpose]
		if !ok {
			purpose = t.Purpose
		}
		page.Tokens = append(page.Tokens, userToken{AdminUserToken: t, Purpose: purpose})
	}

	if err := s.renderHTML(w, http.StatusOK, "user.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
