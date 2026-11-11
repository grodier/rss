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
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// Invites go out in waves: pick people on the waitlist (checkboxes, or "the
// oldest N"), review the wave against the email limits, send (see "Waitlist
// and invites" in docs/decisions.md).

// waitlistPageSize is how many entries the waitlist's page shows at once.
const waitlistPageSize = 100

// maxWaitlistPages is the most pages the waitlist's page can show.
const maxWaitlistPages = 100_000

// maxWave is the most people one wave can invite: a page of the waitlist.
const maxWave = waitlistPageSize

// defaultInviteDays is how long an invite works unless the review says.
const defaultInviteDays = 14

// waitlistTab is one of the waitlist page's tabs.
type waitlistTab struct {
	Key, Label string
	Count      int
	Current    bool
}

// waitlistPage is the waitlist's data.
type waitlistPage struct {
	Nav   navData
	Flash string
	Tab   string
	Tabs  []waitlistTab
	Rows  []rss.WaitlistRow
	// Page is the page number, from 1; Prev and Next are the URLs of the
	// pages before and after it, "" for none.
	Page       int
	Prev, Next string
	// Selectable is whether the rows have checkboxes, on the Waiting tab.
	Selectable bool
	// OldestDefault is the "Select the oldest N" field's value.
	OldestDefault int
	Now           time.Time
}

// waitlistHandler lists the waitlist's ?tab= (waiting by default), oldest
// first, waitlistPageSize entries a ?page=.
func (s *Server) waitlistHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tab := q.Get("tab")
	if tab == "" {
		tab = rss.WaitlistWaiting
	}
	if tab != rss.WaitlistWaiting && tab != rss.WaitlistInvited && tab != rss.WaitlistUnconfirmed {
		s.notFoundResponse(w, r)
		return
	}
	page := 1
	if p := q.Get("page"); p != "" {
		var err error
		// The cap keeps the offset from overflowing; no waitlist is that long.
		if page, err = strconv.Atoi(p); err != nil || page < 1 || page > maxWaitlistPages {
			s.notFoundResponse(w, r)
			return
		}
	}

	counts, err := s.services.Waitlist.Counts(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	// One more than a page, to know whether there's another.
	rows, err := s.services.Waitlist.List(r.Context(), tab, waitlistPageSize+1, (page-1)*waitlistPageSize)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if page > 1 && len(rows) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	data := waitlistPage{
		Nav:   s.nav(r, "waitlist"),
		Flash: s.sessionManager.PopString(r.Context(), sessionFlash),
		Tab:   tab,
		Page:  page,
		Tabs: []waitlistTab{
			{Key: rss.WaitlistWaiting, Label: "Waiting", Count: counts.Waiting},
			{Key: rss.WaitlistInvited, Label: "Invited", Count: counts.Invited},
			{Key: rss.WaitlistUnconfirmed, Label: "Unconfirmed", Count: counts.Unconfirmed},
		},
		Selectable:    tab == rss.WaitlistWaiting,
		OldestDefault: min(counts.Waiting, 10),
		Now:           s.now(),
	}
	for i := range data.Tabs {
		data.Tabs[i].Current = data.Tabs[i].Key == tab
	}
	if len(rows) > waitlistPageSize {
		rows = rows[:waitlistPageSize]
		data.Next = waitlistURL(tab, page+1)
	}
	if page > 1 {
		data.Prev = waitlistURL(tab, page-1)
	}
	data.Rows = rows

	if err := s.renderHTML(w, http.StatusOK, "waitlist.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// waitlistURL is the URL of the waitlist's tab at page.
func waitlistURL(tab string, page int) string {
	v := url.Values{}
	if tab != rss.WaitlistWaiting {
		v.Set("tab", tab)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/waitlist"
	}
	return "/waitlist?" + v.Encode()
}

// reviewForm is what the waitlist posts to the review: the selected entries'
// IDs, or how many of the oldest waiting entries to invite.
type reviewForm struct {
	IDs    []string `form:"id"`
	Oldest string   `form:"oldest"`
}

// waveForm is the review's form, which sends the wave.
type waveForm struct {
	IDs                 []string `form:"id"`
	Wave                string   `form:"wave"`
	Days                string   `form:"days"`
	validator.Validator `form:"-"`
}

// reviewPage is a wave's review: who it invites, its label and expiry, and
// what it does to the email limits. It acts on nothing.
type reviewPage struct {
	Nav     navData
	Entries []rss.WaitlistEntry
	// FitIDs are the IDs of the entries that fit under the limits, the
	// oldest first.
	FitIDs []string
	Form   waveForm
	Check  sendCheck
	// Notice says why the review is shown again instead of the wave being
	// sent, "" the first time.
	Notice string
}

// waitlistReviewHandler shows the review of a wave: the entries posted as
// id, or the oldest N waiting. Entries no longer waiting are left out.
func (s *Server) waitlistReviewHandler(w http.ResponseWriter, r *http.Request) {
	var form reviewForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	var entries []rss.WaitlistEntry
	var err error
	if form.Oldest != "" {
		n, convErr := strconv.Atoi(strings.TrimSpace(form.Oldest))
		if convErr != nil || n < 1 || n > maxWave {
			s.errorHTML(w, r, http.StatusUnprocessableEntity, "Can't review that",
				fmt.Sprintf("Choose between 1 and %d people to invite.", maxWave))
			return
		}
		entries, err = s.services.Waitlist.Oldest(r.Context(), n)
	} else {
		if !s.checkWaveIDs(w, r, form.IDs) {
			return
		}
		entries, err = s.services.Waitlist.ListWaiting(r.Context(), form.IDs)
	}
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	wave := waveForm{Wave: "Wave " + s.now().UTC().Format("2006-01-02"), Days: strconv.Itoa(defaultInviteDays)}
	s.renderReview(w, r, http.StatusOK, entries, wave, "")
}

// checkWaveIDs checks the IDs a wave's form posted: between 1 and maxWave
// UUIDs. When they aren't, it responds and returns false.
func (s *Server) checkWaveIDs(w http.ResponseWriter, r *http.Request, ids []string) bool {
	if len(ids) == 0 || len(ids) > maxWave {
		s.errorHTML(w, r, http.StatusUnprocessableEntity, "Can't review that",
			fmt.Sprintf("Select between 1 and %d people to invite.", maxWave))
		return false
	}
	for _, id := range ids {
		if !validator.Matches(id, validator.UUIDRX) {
			s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That selection isn't valid.")
			return false
		}
	}
	return true
}

// renderReview renders the review of a wave of entries, with the email
// limits as they are now, and form's label and expiry.
func (s *Server) renderReview(w http.ResponseWriter, r *http.Request, status int, entries []rss.WaitlistEntry, form waveForm, notice string) {
	if len(entries) == 0 {
		s.errorHTML(w, r, http.StatusUnprocessableEntity, "No one to invite",
			"Everyone selected has been invited or has left the waitlist.")
		return
	}
	usage, err := s.services.Emails.Usage(r.Context(), s.now())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	check := newSendCheck(s.config.MailLimits, usage, len(entries))
	data := reviewPage{Nav: s.nav(r, "waitlist"), Entries: entries, Form: form, Check: check, Notice: notice}
	for _, e := range entries[:check.Fit()] {
		data.FitIDs = append(data.FitIDs, e.ID)
	}
	if err := s.renderHTML(w, status, "waitlist_review.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// maxWaveLabel is the most characters a wave's label can have.
const maxWaveLabel = 100

// maxInviteDays is the longest an invite can work.
const maxInviteDays = 90

// waitlistInviteHandler sends the wave the review posts: an invite and its
// email to each entry, if they're all still waiting and their emails fit
// under the limits; otherwise it shows the review again, as things are now.
func (s *Server) waitlistInviteHandler(w http.ResponseWriter, r *http.Request) {
	var form waveForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	if !s.checkWaveIDs(w, r, form.IDs) {
		return
	}
	form.Wave = strings.TrimSpace(form.Wave)
	form.CheckField(validator.NotBlank(form.Wave), "wave", "Give the wave a label.")
	form.CheckField(validator.MaxChars(form.Wave, maxWaveLabel), "wave", fmt.Sprintf("Keep the label to %d characters.", maxWaveLabel))
	days, err := strconv.Atoi(strings.TrimSpace(form.Days))
	form.CheckField(err == nil && days >= 1 && days <= maxInviteDays, "days", fmt.Sprintf("Choose between 1 and %d days.", maxInviteDays))

	entries, err := s.services.Waitlist.ListWaiting(r.Context(), form.IDs)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if !form.Valid() {
		s.renderReview(w, r, http.StatusUnprocessableEntity, entries, form, "")
		return
	}
	if len(entries) != len(form.IDs) {
		s.renderReview(w, r, http.StatusConflict, entries, form,
			"Some of them were invited or left the waitlist since you reviewed this wave, so nothing was sent.")
		return
	}

	wave, err := s.newWave(entries, form.Wave, s.now().Add(time.Duration(days)*24*time.Hour))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	err = s.services.Invites.SendWave(r.Context(), wave, s.config.MailLimits.Available, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrOverLimit):
		s.renderReview(w, r, http.StatusConflict, entries, form,
			"More emails were sent since you reviewed this wave, so nothing was sent. Here are the limits now.")
		return
	case errors.Is(err, rss.ErrNoRecord):
		entries, err := s.services.Waitlist.ListWaiting(r.Context(), form.IDs)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		s.renderReview(w, r, http.StatusConflict, entries, form,
			"Some of them were invited or left the waitlist since you reviewed this wave, so nothing was sent.")
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}

	n := len(wave.Invites)
	s.sessionManager.Put(r.Context(), sessionFlash, fmt.Sprintf("Sent %d %s in %s.", n, plural(n, "invite", "invites"), wave.Label))
	http.Redirect(w, r, waitlistURL(rss.WaitlistInvited, 1), http.StatusSeeOther)
}

// newWave makes the wave of invites to entries, each with its email.
func (s *Server) newWave(entries []rss.WaitlistEntry, label string, expiresAt time.Time) (rss.Wave, error) {
	wave := rss.Wave{Label: label, ExpiresAt: expiresAt}
	for _, e := range entries {
		inv, _, err := s.newInviteEmail(e.ID, e.Email, e.Name, expiresAt)
		if err != nil {
			return rss.Wave{}, err
		}
		wave.Invites = append(wave.Invites, inv)
	}
	return wave, nil
}

// newInviteEmail makes an invite's token and renders its email to email,
// greeting name, expiring at expiresAt. With a waitlist entry (waitlistID),
// it also makes the entry's new link token, for the email's leave link. It
// returns the invite with both tokens' hashes and the email, and its signup
// link.
func (s *Server) newInviteEmail(waitlistID, email, name string, expiresAt time.Time) (rss.WaveInvite, string, error) {
	invite, inviteHash, err := token.New()
	if err != nil {
		return rss.WaveInvite{}, "", err
	}
	data := inviteEmail{
		Name:      name,
		SignupURL: s.config.AppURL + "/signup?invite=" + invite,
		ExpiresOn: expiresAt.UTC().Format("2 January 2006"),
	}
	inv := rss.WaveInvite{WaitlistID: waitlistID, Email: email, InviteTokenHash: inviteHash}
	if waitlistID != "" {
		leave, leaveHash, err := token.New()
		if err != nil {
			return rss.WaveInvite{}, "", err
		}
		data.LeaveURL = s.config.AppURL + "/waitlist/leave?token=" + leave
		inv.WaitlistTokenHash = leaveHash
	}
	m, err := s.emails.Render("invite", data)
	if err != nil {
		return rss.WaveInvite{}, "", err
	}
	inv.Subject, inv.Text, inv.HTML = m.Subject, m.Text, m.HTML
	return inv, data.SignupURL, nil
}

// inviteEmail is the invite email's data (internal/ui/emails/invite.tmpl):
// Name and LeaveURL are "" for an invite with no waitlist entry.
type inviteEmail struct {
	Name, SignupURL, ExpiresOn, LeaveURL string
}

// plural is one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// waitlistStatus says where row stands at now: unconfirmed, waiting, or
// invited, with its latest invite's expiry, or why a past invite didn't
// lead to an account.
func waitlistStatus(row rss.WaitlistRow, now time.Time) string {
	if row.ConfirmedAt.IsZero() {
		return "Unconfirmed"
	}
	if row.Invite.ID == "" {
		return "Waiting"
	}
	switch row.Invite.Status(now) {
	case rss.InvitePending:
		return "Invited until " + row.Invite.ExpiresAt.UTC().Format("2006-01-02")
	case rss.InviteExpired:
		return "Invite expired " + row.Invite.ExpiresAt.UTC().Format("2006-01-02")
	case rss.InviteRevoked:
		return "Invite revoked"
	}
	return "Waiting"
}

// inviteStatus is inv's status at now, in words.
func inviteStatus(inv rss.Invite, now time.Time) string {
	switch inv.Status(now) {
	case rss.InviteRedeemed:
		return "Signed up"
	case rss.InviteExpired:
		return "Expired"
	case rss.InviteRevoked:
		return "Revoked"
	}
	return "Pending"
}
