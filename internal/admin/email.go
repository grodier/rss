package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The email page shows the outbox, so when an email doesn't arrive the
// owner sees whether it was sent, failed or is still waiting, and how much
// of the provider's limits is used. It never shows an email's bodies,
// which can carry one-time links. Its actions, retrying or canceling a
// pending email and queuing a test email, are audited by their repository
// methods ("Admin" in docs/decisions.md).

// emailPageSize is how many emails the email page shows at once.
const emailPageSize = 100

// sessionFlashEmail is the ID of the test email just queued, which the
// email page's flash links to in its table.
const sessionFlashEmail = "flashEmail"

// emailFilters are the email page's filters, in order: all, the default,
// then each status.
var emailFilters = []struct{ Key, Label string }{
	{"", "All"},
	{rss.EmailPending, "Pending"},
	{rss.EmailSent, "Sent"},
	{rss.EmailFailed, "Failed"},
}

// emailPage is the email page's data.
type emailPage struct {
	Nav   navData
	Flash string
	// FlashEmail is the email the flash is about, linked to its row; "" for
	// none.
	FlashEmail string
	Usage      []statTile
	Filters    []filterLink
	Status     string
	Emails     []emailRow
	// Next is the URL of the page of older emails, "" on the last page.
	Next string
	Form testEmailForm
	Now  time.Time
}

// emailRow is an outbox email as the page lists it: its status in words
// (Label) and its tone (emailStatuses, as a reader's page says them).
type emailRow struct {
	rss.AdminEmail
	Label, Tone string
}

// Retryable reports whether Retry now is offered: the email is pending
// and has been tried, so it's waiting for its next attempt. A pending
// email not yet tried is due already.
func (e emailRow) Retryable() bool {
	return e.Status == rss.EmailPending && e.Attempts > 0
}

// emailHref is the email page's URL with status and after, leaving out
// the defaults.
func emailHref(status, after string) string {
	v := url.Values{}
	if status != "" {
		v.Set("status", status)
	}
	if after != "" {
		v.Set("after", after)
	}
	if len(v) == 0 {
		return "/email"
	}
	return "/email?" + v.Encode()
}

// usageTiles are the outbox's usage against the provider's limits, l:
// today's and this month's emails, the reserve kept for account emails,
// and how many more invites can send.
func usageTiles(l MailLimits, u rss.EmailUsage) []statTile {
	return []statTile{
		{Label: "Used today", Value: u.Last24h, Note: "of " + count(l.Daily) + " a day, in the last 24 hours"},
		{Label: "Used this month", Value: u.ThisMonth, Note: "of " + count(l.Monthly) + " a month, UTC"},
		{Label: "Kept for account emails", Value: l.Reserve, Note: "a day and a month, which invites leave"},
		{Label: "Invites can send", Value: l.Available(u), Note: "more now, under both limits"},
	}
}

// emailHandler shows the outbox's usage and emails, newest first,
// emailPageSize at a time, filtered by ?status= (all by default, as is a
// status the page doesn't offer), from after the email ?after=.
func (s *Server) emailHandler(w http.ResponseWriter, r *http.Request) {
	s.renderEmail(w, r, http.StatusOK, testEmailForm{})
}

// renderEmail renders the email page with status and form, the test
// email's form as posted.
func (s *Server) renderEmail(w http.ResponseWriter, r *http.Request, status int, form testEmailForm) {
	q := r.URL.Query()
	filter := ""
	for _, f := range emailFilters {
		if q.Get("status") == f.Key {
			filter = f.Key
		}
	}
	after := q.Get("after")
	if after != "" && !validator.Matches(after, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	// One more than a page, to know whether there's another.
	emails, err := s.services.Emails.List(r.Context(), rss.AdminEmailQuery{Status: filter, After: after, Limit: emailPageSize + 1})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if after != "" && len(emails) == 0 {
		s.notFoundResponse(w, r)
		return
	}
	counts, err := s.services.Emails.CountByStatus(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	now := s.now()
	usage, err := s.services.Emails.Usage(r.Context(), now)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := emailPage{
		Nav:    s.nav(r, "email"),
		Usage:  usageTiles(s.config.MailLimits, usage),
		Status: filter,
		Form:   form,
		Now:    now,
	}
	if status == http.StatusOK {
		page.Flash = s.sessionManager.PopString(r.Context(), sessionFlash)
		page.FlashEmail = s.sessionManager.PopString(r.Context(), sessionFlashEmail)
	}
	if len(emails) > emailPageSize {
		emails = emails[:emailPageSize]
		page.Next = emailHref(filter, emails[len(emails)-1].ID)
	}
	for _, f := range emailFilters {
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: emailHref(f.Key, ""), Current: f.Key == filter, Count: count(counts[f.Key])})
	}
	for _, e := range emails {
		st := emailStatuses[e.Status]
		page.Emails = append(page.Emails, emailRow{AdminEmail: e, Label: st.Text, Tone: st.Tone})
	}

	if err := s.renderHTML(w, status, "email.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// emailActionDone sends the admin back to the email page saying how an
// action on the email {id} went: done when err is nil.
func (s *Server) emailActionDone(w http.ResponseWriter, r *http.Request, err error, done string) {
	switch {
	case errors.Is(err, rss.ErrEmailNotPending):
		s.backTo(w, r, "/email", "That email isn’t pending any more: it was sent or given up on.")
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.backTo(w, r, "/email", done)
	}
}

// emailIDFromURL returns the URL's {id}; when it isn't an email's ID, it
// responds with 404 and returns false.
func (s *Server) emailIDFromURL(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return "", false
	}
	return id, true
}

// emailRetryHandler makes the pending email {id} due now with its attempts
// reset, recorded as email.retry. A failed email's bodies are cleared, so
// it can't be retried: the page says to send a new one instead.
func (s *Server) emailRetryHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := s.emailIDFromURL(w, r)
	if !ok {
		return
	}
	err := s.services.Emails.Retry(r.Context(), id, s.actor(r))
	s.emailActionDone(w, r, err, "Retrying: www’s mail runner sends it within a minute.")
}

// emailCancelHandler gives up on the pending email {id}, clearing its
// bodies, recorded as email.cancel.
func (s *Server) emailCancelHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := s.emailIDFromURL(w, r)
	if !ok {
		return
	}
	err := s.services.Emails.Cancel(r.Context(), id, s.actor(r))
	s.emailActionDone(w, r, err, "Canceled: it won’t be sent.")
}

// testEmailForm is the address to send the test email to.
type testEmailForm struct {
	To                  string `form:"to"`
	validator.Validator `form:"-"`
}

// emailTestHandler renders the test email, the one admin send-test-email
// sends, and queues it to the posted address, recorded as email.send_test,
// so www's mail runner sends it like any other email.
func (s *Server) emailTestHandler(w http.ResponseWriter, r *http.Request) {
	var form testEmailForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	form.To = strings.TrimSpace(form.To)
	form.CheckField(validator.NotBlank(form.To), "to", "Enter an email address.")
	if form.FieldErrors["to"] == "" {
		form.CheckField(validator.Matches(form.To, validator.EmailRX), "to", "Enter an email address, like you@example.com.")
	}
	if !form.Valid() {
		s.renderEmail(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	m, err := s.emails.Render("test", nil)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	m.To = form.To
	id, err := s.services.Emails.QueueTest(r.Context(), m, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrTooManyEmails):
		s.backTo(w, r, "/email", tooManyEmailsMessage(form.To))
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.sessionManager.Put(r.Context(), sessionFlashEmail, id)
		s.backTo(w, r, "/email", "Test email queued to "+form.To+".")
	}
}
