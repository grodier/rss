package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
// row the page starts after, as userCursor writes it ("" for the first
// page).
type usersParams struct {
	q, after string
}

// userCursor is ?after= for the page after row: its join time in Unix
// seconds (users.created_at is to the second) and its ID.
func userCursor(row rss.AdminUserRow) string {
	return strconv.FormatInt(row.CreatedAt.Unix(), 10) + "_" + row.ID
}

// parseUserCursor reads a userCursor, reporting false when s isn't one.
func parseUserCursor(s string) (rss.AdminUserCursor, bool) {
	secs, id, ok := strings.Cut(s, "_")
	if !ok || !validator.Matches(id, validator.UUIDRX) {
		return rss.AdminUserCursor{}, false
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return rss.AdminUserCursor{}, false
	}
	return rss.AdminUserCursor{CreatedAt: time.Unix(n, 0), ID: id}, true
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
	Nav   navData
	Flash string
	Q     string
	Rows  []rss.AdminUserRow
	Now   time.Time
	// Next is the URL of the next page, "" on the last.
	Next string
}

// usersHandler lists readers, newest first, usersPageSize at a time,
// those whose email or name starts with ?q= (any case), from after the
// row ?after=.
func (s *Server) usersHandler(w http.ResponseWriter, r *http.Request) {
	params := usersParams{q: strings.TrimSpace(r.URL.Query().Get("q")), after: r.URL.Query().Get("after")}
	var after rss.AdminUserCursor
	if params.after != "" {
		var ok bool
		if after, ok = parseUserCursor(params.after); !ok {
			s.notFoundResponse(w, r)
			return
		}
	}
	// One more than a page, to know whether there's another.
	rows, err := s.services.Users.ListUsers(r.Context(), rss.AdminUserQuery{Q: params.q, After: after, Limit: usersPageSize + 1})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if params.after != "" && len(rows) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	page := usersPage{Nav: s.nav(r, "users"), Flash: s.sessionManager.PopString(r.Context(), sessionFlash), Q: params.q, Now: s.now()}
	if len(rows) > usersPageSize {
		rows = rows[:usersPageSize]
		next := params
		next.after = userCursor(rows[len(rows)-1])
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
	Nav   navData
	Flash string
	User  rss.AdminUser
	// Counts are the reader's counts as a stat row.
	Counts []statTile
	// Emails are the outbox's emails to the reader, and Tokens their
	// outstanding email links, as the page says them.
	Emails []userEmail
	Tokens []userToken
	// History is the reader's entries in the audit log.
	History string
	Now     time.Time
}

// userEmail is an email in the outbox as a reader's page shows it: its
// status in words (Label) and its tone (emailStatuses).
type userEmail struct {
	rss.AdminUserEmail
	Label, Tone string
}

// userToken is an outstanding email link as a reader's page shows it: its
// purpose in words (Label).
type userToken struct {
	rss.AdminUserToken
	Label string
}

// tokenPurposes say what each kind of email link is for (user_tokens'
// purposes, all of them).
var tokenPurposes = map[string]string{
	rss.TokenConfirmEmail:  "Confirm email",
	rss.TokenResetPassword: "Reset password",
	rss.TokenChangeEmail:   "Change email",
}

// emailStatuses say each of an outbox email's statuses (all of them), and
// its tone, a status label's class suffix: an invite's tones for waiting
// and done (status-pending, status-redeemed), and the danger tone when the
// outbox gave up.
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

// userFromURL returns the reader the URL's {id} names; when there's none,
// it responds with 404 and returns false.
func (s *Server) userFromURL(w http.ResponseWriter, r *http.Request) (rss.AdminUser, bool) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return rss.AdminUser{}, false
	}
	user, err := s.services.Users.User(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return rss.AdminUser{}, false
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return rss.AdminUser{}, false
	}
	return user, true
}

// userHandler shows the reader {id}'s account, counts and emails, and the
// actions on it.
func (s *Server) userHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}

	page := userPage{
		Nav:     s.nav(r, "users"),
		Flash:   s.sessionManager.PopString(r.Context(), sessionFlash),
		User:    user,
		Counts:  userCounts(user.Counts),
		History: auditHistoryURL("user", user.ID),
		Now:     s.now(),
	}
	for _, e := range user.Emails {
		st := emailStatuses[e.Status]
		page.Emails = append(page.Emails, userEmail{AdminUserEmail: e, Label: st.Text, Tone: st.Tone})
	}
	for _, t := range user.Tokens {
		page.Tokens = append(page.Tokens, userToken{AdminUserToken: t, Label: tokenPurposes[t.Purpose]})
	}

	if err := s.renderHTML(w, http.StatusOK, "user.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
