package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// The invites: every invite with each wave's summary, revoking and
// resending one, and inviting someone by hand (see "Waitlist and invites"
// in docs/decisions.md). Every email they send is checked against the
// limits, like a wave.

// invitesPageSize is how many invites the invites page shows at once.
const invitesPageSize = 100

// maxInviteNote is the most characters a hand-made invite's note can have.
const maxInviteNote = 200

// overLimitMessage is what a send that doesn't fit under the limits says.
const overLimitMessage = "No more emails fit today without risking account emails."

// invitesPage lists every invite, newest first, with each wave's summary
// on the first page.
type invitesPage struct {
	Nav     navData
	Flash   string
	Waves   []rss.InviteWave
	Invites []rss.Invite
	// Next is the URL of the page of older invites, "" on the last page.
	Next string
	Now  time.Time
}

// invitesHandler shows the waves' summaries and every invite, newest first,
// invitesPageSize at a time, from after the invite ?before=.
func (s *Server) invitesHandler(w http.ResponseWriter, r *http.Request) {
	before := r.URL.Query().Get("before")
	if before != "" && !validator.Matches(before, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}
	// One more than a page, to know whether there's another.
	invites, err := s.services.Invites.List(r.Context(), before, invitesPageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if before != "" && len(invites) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	data := invitesPage{Nav: s.nav(r, "waitlist"), Flash: s.sessionManager.PopString(r.Context(), sessionFlash), Now: s.now()}
	if len(invites) > invitesPageSize {
		invites = invites[:invitesPageSize]
		data.Next = "/invites?before=" + invites[len(invites)-1].ID
	}
	data.Invites = invites
	if before == "" {
		if data.Waves, err = s.services.Invites.Waves(r.Context()); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}
	if err := s.renderHTML(w, http.StatusOK, "invites.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// inviteFromURL returns the invite the URL's {id} names; when there's none,
// it responds with 404 and returns false.
func (s *Server) inviteFromURL(w http.ResponseWriter, r *http.Request) (rss.Invite, bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.Invite{}, false
	}
	inv, err := s.services.Invites.Get(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return rss.Invite{}, false
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return rss.Invite{}, false
	}
	return inv, true
}

// revokedMessage is the flash after revoking inv, naming who it was for:
// its email, or its note.
func revokedMessage(inv rss.Invite) string {
	switch {
	case inv.Email != "":
		return "Revoked the invite to " + inv.Email + "."
	case inv.Note != "":
		return "Revoked the invite to " + inv.Note + "."
	}
	return "Revoked the open invite."
}

// backToInvites redirects to the invites page with msg as its flash.
func (s *Server) backToInvites(w http.ResponseWriter, r *http.Request, msg string) {
	s.sessionManager.Put(r.Context(), sessionFlash, msg)
	http.Redirect(w, r, "/invites", http.StatusSeeOther)
}

// inviteRevokeHandler revokes the invite {id}, so its link stops working.
func (s *Server) inviteRevokeHandler(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inviteFromURL(w, r)
	if !ok {
		return
	}
	err := s.services.Invites.Revoke(r.Context(), inv.ID, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrInviteUnusable):
		s.backToInvites(w, r, "That invite has been used, so it can't be revoked.")
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.backToInvites(w, r, revokedMessage(inv))
	}
}

// inviteResendHandler gives the bound invite {id} a new link, expiring
// defaultInviteDays from now, and emails it again.
func (s *Server) inviteResendHandler(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inviteFromURL(w, r)
	if !ok {
		return
	}
	expiresAt := s.now().Add(defaultInviteDays * 24 * time.Hour)
	again, _, err := s.newInviteEmail(inv.WaitlistID, inv.Email, inv.Name, expiresAt)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	err = s.services.Invites.Resend(r.Context(), inv.ID, again, expiresAt, s.config.MailLimits.Available, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrInviteUnusable):
		s.backToInvites(w, r, "That invite can't be resent: it's open, used or revoked.")
	case errors.Is(err, rss.ErrOverLimit):
		s.backToInvites(w, r, overLimitMessage)
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.backToInvites(w, r, "Sent a new invite to "+inv.Email+".")
	}
}

// newInviteForm invites someone by hand.
type newInviteForm struct {
	Email               string `form:"email"`
	Note                string `form:"note"`
	Days                string `form:"days"`
	validator.Validator `form:"-"`
}

// newInvitePage is the form to invite someone by hand and, once it's made,
// the invite's link.
type newInvitePage struct {
	Nav  navData
	Form newInviteForm
	// Link is the new invite's link, "" until it's made; Emailed, whether
	// it was also emailed; ExpiresOn, when it stops working.
	Link      string
	Emailed   bool
	ExpiresOn string
}

// newInviteHandler shows the form to invite someone by hand.
func (s *Server) newInviteHandler(w http.ResponseWriter, r *http.Request) {
	s.renderNewInvite(w, r, http.StatusOK, newInvitePage{Form: newInviteForm{Days: strconv.Itoa(defaultInviteDays)}})
}

func (s *Server) renderNewInvite(w http.ResponseWriter, r *http.Request, status int, data newInvitePage) {
	data.Nav = s.nav(r, "waitlist")
	if err := s.renderHTML(w, status, "invite_new.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// newInviteFormHandler makes an invite by hand. With an email, it's bound
// to that confirmed, waiting waitlist entry and emailed, checked against the
// limits; without, it's open. Either way the page then shows its link,
// which isn't kept.
func (s *Server) newInviteFormHandler(w http.ResponseWriter, r *http.Request) {
	var form newInviteForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	form.Email = strings.TrimSpace(form.Email)
	form.Note = strings.TrimSpace(form.Note)
	if form.Email != "" {
		form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Enter an email address, or leave it empty.")
	}
	form.CheckField(validator.MaxChars(form.Note, maxInviteNote), "note", fmt.Sprintf("Keep it to %d characters.", maxInviteNote))
	days, err := strconv.Atoi(strings.TrimSpace(form.Days))
	form.CheckField(err == nil && days >= 1 && days <= maxInviteDays, "days", fmt.Sprintf("Choose between 1 and %d days.", maxInviteDays))

	// A bound invite's account starts confirmed, so only an address
	// confirmed on the waitlist can have one, and only once at a time.
	var entry rss.WaitlistEntry
	if form.Email != "" && form.FieldErrors["email"] == "" {
		entry, err = s.services.Waitlist.GetByEmail(r.Context(), form.Email)
		switch {
		case errors.Is(err, rss.ErrNoRecord) || (err == nil && entry.ConfirmedAt.IsZero()):
			form.AddFieldError("email", "No one with this email is confirmed on the waitlist. Leave it empty for an open invite.")
		case err != nil:
			s.serverErrorHTML(w, r, err)
			return
		default:
			waiting, err := s.services.Waitlist.ListWaiting(r.Context(), []string{entry.ID})
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
			if len(waiting) == 0 {
				form.AddFieldError("email", alreadyInvited(entry.Email))
			}
		}
	}
	if !form.Valid() {
		s.renderNewInvite(w, r, http.StatusUnprocessableEntity, newInvitePage{Form: form})
		return
	}

	expiresAt := s.now().Add(time.Duration(days) * 24 * time.Hour)
	data := newInvitePage{Form: form, ExpiresOn: expiresAt.UTC().Format("2 January 2006")}
	if form.Email == "" {
		plain, hash, err := token.New()
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		inv := rss.Invite{Note: form.Note, ExpiresAt: expiresAt}
		if _, err := s.services.Invites.Create(r.Context(), inv, hash, s.actor(r)); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Link = s.config.AppURL + "/signup?invite=" + plain
		s.renderNewInvite(w, r, http.StatusOK, data)
		return
	}

	inv, link, err := s.newInviteEmail(entry.ID, entry.Email, entry.Name, expiresAt)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	_, err = s.services.Invites.SendInvite(r.Context(), inv, form.Note, expiresAt, s.config.MailLimits.Available, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrOverLimit):
		form.AddNonFieldError(overLimitMessage)
		s.renderNewInvite(w, r, http.StatusUnprocessableEntity, newInvitePage{Form: form})
		return
	case errors.Is(err, rss.ErrNoRecord):
		// Invited, or gone, since the checks above.
		form.AddFieldError("email", alreadyInvited(entry.Email))
		s.renderNewInvite(w, r, http.StatusUnprocessableEntity, newInvitePage{Form: form})
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}
	data.Form.Email, data.Link, data.Emailed = entry.Email, link, true
	s.renderNewInvite(w, r, http.StatusOK, data)
}

// alreadyInvited says email has an invite to use already.
func alreadyInvited(email string) string {
	return email + " already has an invite waiting to be used: resend it from the invites page."
}
