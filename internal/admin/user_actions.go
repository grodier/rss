package admin

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/accounts"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// A reader's page's actions, for answering support emails ("I never got
// the confirmation", "I can't get in", "please delete my account"). Each is
// recorded in the audit log by its repository method; none logs in as the
// reader ("Admin" in docs/decisions.md).

// alreadyConfirmedMessage is what Resend confirmation and Mark confirmed say
// about a confirmed address.
const alreadyConfirmedMessage = "Their email address is already confirmed."

// addressChangedMessage is what an action about the address the page showed
// says when the account's address is no longer that one.
const addressChangedMessage = "Their email address changed since the page loaded: check it, then try again."

// tooManyEmailsMessage says no email went to address, which is over the
// outbox's per-address cap.
func tooManyEmailsMessage(address string) string {
	return "Nothing sent: " + address + " has had too many emails in the last hour. Try again later."
}

// userBack sends the admin back to user's page with msg.
func (s *Server) userBack(w http.ResponseWriter, r *http.Request, user rss.AdminUser, msg string) {
	s.backTo(w, r, "/users/"+user.ID, msg)
}

// addressForm is an action about the reader's address: the address their
// page showed, which the action uses, and its repository method refuses
// when it's no longer the account's (rss.ErrAddressChanged), so it never
// reaches an address the admin didn't see.
type addressForm struct {
	Email string `form:"email"`
}

// userAddressAction returns the reader {id} and the address the form names.
// When there's no reader, or the form doesn't decode, it responds and
// returns false.
func (s *Server) userAddressAction(w http.ResponseWriter, r *http.Request) (rss.AdminUser, string, bool) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return rss.AdminUser{}, "", false
	}
	var form addressForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return rss.AdminUser{}, "", false
	}
	return user, form.Email, true
}

// addressActionDone sends the admin back to user's page saying how the
// action about address went: sent when err is nil.
func (s *Server) addressActionDone(w http.ResponseWriter, r *http.Request, user rss.AdminUser, address string, err error, sent string) {
	switch {
	case errors.Is(err, rss.ErrAlreadyConfirmed):
		s.userBack(w, r, user, alreadyConfirmedMessage)
	case errors.Is(err, rss.ErrAddressChanged):
		s.userBack(w, r, user, addressChangedMessage)
	case errors.Is(err, rss.ErrTooManyEmails):
		s.userBack(w, r, user, tooManyEmailsMessage(address))
	case errors.Is(err, rss.ErrNoRecord):
		// Deleted since the page loaded.
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.userBack(w, r, user, sent)
	}
}

// sendUserLink emails the link newLink makes, an accounts.Emails method,
// to the address the form names, as addressForm says.
func (s *Server) sendUserLink(w http.ResponseWriter, r *http.Request, newLink func(rss.User, time.Time) (accounts.Link, error), sent string) {
	user, address, ok := s.userAddressAction(w, r)
	if !ok {
		return
	}
	link, err := newLink(rss.User{ID: user.ID, Name: user.Name, Email: address}, s.now())
	if err == nil {
		err = s.services.Users.SendLink(r.Context(), link.Token, link.Email, s.actor(r))
	}
	s.addressActionDone(w, r, user, address, err, fmt.Sprintf(sent, address))
}

// userResendConfirmationHandler emails the reader {id} a new link to
// confirm their address, the email www sends at signup, recorded as
// user.resend_confirmation. Its repository method refuses a confirmed
// address.
func (s *Server) userResendConfirmationHandler(w http.ResponseWriter, r *http.Request) {
	s.sendUserLink(w, r, s.accountEmails.Confirmation, "Confirmation sent to %s.")
}

// userSendPasswordResetHandler emails the reader {id} a link to choose a
// new password, the email of www's "Forgot password?", recorded as
// user.send_password_reset.
func (s *Server) userSendPasswordResetHandler(w http.ResponseWriter, r *http.Request) {
	s.sendUserLink(w, r, s.accountEmails.PasswordReset, "Reset link sent to %s.")
}

// userMarkConfirmedHandler confirms the reader {id}'s address, the one the
// form names, without a link, for one confirmed another way (by email to
// support), recorded as user.mark_confirmed. Its repository method refuses a
// confirmed address.
func (s *Server) userMarkConfirmedHandler(w http.ResponseWriter, r *http.Request) {
	user, address, ok := s.userAddressAction(w, r)
	if !ok {
		return
	}
	err := s.services.Users.MarkConfirmed(r.Context(), user.ID, address, s.actor(r))
	s.addressActionDone(w, r, user, address, err, "Marked "+address+" as confirmed.")
}

// userLogOutEverywhereHandler ends all of the reader {id}'s sessions,
// recorded as user.log_out_everywhere.
func (s *Server) userLogOutEverywhereHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	n, err := s.services.Users.LogOutEverywhere(r.Context(), user.ID, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	case n == 0:
		s.userBack(w, r, user, "They weren't logged in on any device.")
	default:
		s.userBack(w, r, user, fmt.Sprintf("Logged out of %s.", plural(n, "1 device", count(n)+" devices")))
	}
}

// userDeletePage is the page that deletes a reader's account.
type userDeletePage struct {
	Nav  navData
	User rss.AdminUser
	Form userDeleteForm
}

// userDeleteForm is the delete's typed confirmation: the account's email.
type userDeleteForm struct {
	Email               string `form:"email"`
	validator.Validator `form:"-"`
}

func (s *Server) renderUserDelete(w http.ResponseWriter, r *http.Request, status int, data userDeletePage) {
	data.Nav = s.nav(r, "users")
	if err := s.renderHTML(w, status, "user_delete.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// userDeleteHandler asks to confirm deleting the reader {id}'s account by
// typing its email. It's behind requireRecentCode.
func (s *Server) userDeleteHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	s.renderUserDelete(w, r, http.StatusOK, userDeletePage{User: user})
}

// userDeleteFormHandler deletes the reader {id}'s account, without their
// password, once the admin has typed its email, recorded as user.delete;
// then it emails the old address that the account is gone, as the
// reader's own delete does. It's behind requireRecentCode.
func (s *Server) userDeleteFormHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	var form userDeleteForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	if !confirmDestructive(&form.Validator, "email", form.Email, user.Email) {
		s.renderUserDelete(w, r, http.StatusUnprocessableEntity, userDeletePage{User: user, Form: form})
		return
	}

	old, err := s.services.Users.Delete(r.Context(), user.ID, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}

	// The account is gone either way, so the flash says when the email
	// couldn't be queued rather than failing the page.
	msg := "Account deleted. We emailed " + old.Email + " to say so."
	m, err := s.accountEmails.AccountDeleted(old)
	if err == nil {
		err = s.services.Emails.Enqueue(r.Context(), m)
	}
	switch {
	case errors.Is(err, rss.ErrTooManyEmails):
		msg = "Account deleted. We couldn't email " + old.Email + ": it has had too many emails in the last hour."
	case err != nil:
		s.logError(r, err)
		msg = "Account deleted. The email to " + old.Email + " to say so couldn't be queued."
	}
	s.backTo(w, r, "/users", msg)
}
