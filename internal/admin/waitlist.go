package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// Invites go out in waves: pick people on the waitlist (checkboxes, or "the
// oldest N"), review the wave against the email limits, send (see "Waitlist
// and invites" in docs/decisions.md).

// waitlistPageSize is how many entries the waitlist's page shows at once.
const waitlistPageSize = 100

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
	Nav  navData
	Tab  string
	Tabs []waitlistTab
	Rows []rss.WaitlistRow
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
		if page, err = strconv.Atoi(p); err != nil || page < 1 {
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
		Nav:  s.nav(r, "waitlist"),
		Tab:  tab,
		Page: page,
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
	validator.Validator
}

// reviewPage is a wave's review: who it invites, its label and expiry, and
// what it does to the email limits. It acts on nothing.
type reviewPage struct {
	Nav     navData
	Entries []rss.WaitlistEntry
	// FitIDs are the IDs of the entries that fit under the limits, the
	// oldest first.
	FitIDs []string
	Wave   string
	Days   int
	Check  sendCheck
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
	switch {
	case form.Oldest != "":
		n, convErr := strconv.Atoi(strings.TrimSpace(form.Oldest))
		if convErr != nil || n < 1 || n > maxWave {
			s.errorHTML(w, r, http.StatusUnprocessableEntity, "Can't review that",
				fmt.Sprintf("Choose between 1 and %d people to invite.", maxWave))
			return
		}
		entries, err = s.services.Waitlist.Oldest(r.Context(), n)
	case len(form.IDs) > 0 && len(form.IDs) <= maxWave:
		for _, id := range form.IDs {
			if !validator.Matches(id, validator.UUIDRX) {
				s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That selection isn't valid.")
				return
			}
		}
		entries, err = s.services.Waitlist.ListWaiting(r.Context(), form.IDs)
	default:
		s.errorHTML(w, r, http.StatusUnprocessableEntity, "Can't review that",
			fmt.Sprintf("Select between 1 and %d people to invite.", maxWave))
		return
	}
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
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
	data := reviewPage{
		Nav:     s.nav(r, "waitlist"),
		Entries: entries,
		Wave:    "Wave " + s.now().UTC().Format("2006-01-02"),
		Days:    defaultInviteDays,
		Check:   check,
	}
	for _, e := range entries[:check.Fit()] {
		data.FitIDs = append(data.FitIDs, e.ID)
	}
	if err := s.renderHTML(w, http.StatusOK, "waitlist_review.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// invitesPage lists every invite, with a summary of each wave.
type invitesPage struct {
	Nav     navData
	Waves   []rss.InviteWave
	Invites []rss.Invite
	Now     time.Time
}

// invitesHandler shows every invite, newest first, and each wave's summary.
func (s *Server) invitesHandler(w http.ResponseWriter, r *http.Request) {
	waves, err := s.services.Invites.Waves(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	invites, err := s.services.Invites.List(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := invitesPage{Nav: s.nav(r, "waitlist"), Waves: waves, Invites: invites, Now: s.now()}
	if err := s.renderHTML(w, http.StatusOK, "invites.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// newInviteForm invites someone by hand.
type newInviteForm struct {
	Email               string `form:"email"`
	Note                string `form:"note"`
	Days                string `form:"days"`
	validator.Validator `form:"-"`
}

// newInvitePage is the form to invite someone by hand, and, once sent, the
// invite's link.
type newInvitePage struct {
	Nav  navData
	Form newInviteForm
	// Link is the new invite's link, "" until it's made; Emailed, whether it
	// was also emailed.
	Link    string
	Emailed bool
}

// newInviteHandler shows the form to invite someone by hand.
func (s *Server) newInviteHandler(w http.ResponseWriter, r *http.Request) {
	data := newInvitePage{Nav: s.nav(r, "waitlist"), Form: newInviteForm{Days: strconv.Itoa(defaultInviteDays)}}
	if err := s.renderHTML(w, http.StatusOK, "invite_new.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
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
