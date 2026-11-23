package admin

import (
	"errors"
	"fmt"
	"net/http"

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

// tooManyEmailsMessage says no email went to address, which is over the
// outbox's per-address cap.
func tooManyEmailsMessage(address string) string {
	return "Nothing sent: " + address + " has had too many emails in the last hour. Try again later."
}

// userBack sends the admin back to user's page with msg.
func (s *Server) userBack(w http.ResponseWriter, r *http.Request, user rss.AdminUser, msg string) {
	s.backTo(w, r, "/users/"+user.ID, msg)
}

// sendUserLink emails user the link made, saying sent when it's queued.
func (s *Server) sendUserLink(w http.ResponseWriter, r *http.Request, user rss.AdminUser, link accounts.Link, err error, sent string) {
	if err == nil {
		err = s.services.Users.SendLink(r.Context(), link.Token, link.Email, s.actor(r))
	}
	switch {
	case errors.Is(err, rss.ErrAlreadyConfirmed):
		s.userBack(w, r, user, alreadyConfirmedMessage)
	case errors.Is(err, rss.ErrTooManyEmails):
		s.userBack(w, r, user, tooManyEmailsMessage(user.Email))
	case errors.Is(err, rss.ErrNoRecord):
		// Deleted, or their address changed, since the page was loaded.
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.userBack(w, r, user, sent)
	}
}

// accountUser is the reader as the account emails greet them.
func accountUser(u rss.AdminUser) rss.User {
	return rss.User{ID: u.ID, Name: u.Name, Email: u.Email}
}

// userResendConfirmationHandler emails the reader {id} a new link to
// confirm their address, the email www sends at signup, recorded as
// user.resend_confirmation. It refuses a confirmed address.
func (s *Server) userResendConfirmationHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if !user.EmailConfirmedAt.IsZero() {
		s.userBack(w, r, user, alreadyConfirmedMessage)
		return
	}
	link, err := s.accountEmails.Confirmation(accountUser(user), s.now())
	s.sendUserLink(w, r, user, link, err, "Confirmation sent to "+user.Email+".")
}

// userSendPasswordResetHandler emails the reader {id} a link to choose a
// new password, the email of www's "Forgot password?", recorded as
// user.send_password_reset.
func (s *Server) userSendPasswordResetHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	link, err := s.accountEmails.PasswordReset(accountUser(user), s.now())
	s.sendUserLink(w, r, user, link, err, "Reset link sent to "+user.Email+".")
}

// userMarkConfirmedHandler confirms the reader {id}'s address without a
// link, for one confirmed another way (by email to support), recorded as
// user.mark_confirmed. It refuses a confirmed address.
func (s *Server) userMarkConfirmedHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userFromURL(w, r)
	if !ok {
		return
	}
	if !user.EmailConfirmedAt.IsZero() {
		s.userBack(w, r, user, alreadyConfirmedMessage)
		return
	}
	_, err := s.services.Users.MarkConfirmed(r.Context(), user.ID, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrAlreadyConfirmed):
		s.userBack(w, r, user, alreadyConfirmedMessage)
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.userBack(w, r, user, "Marked "+user.Email+" as confirmed.")
	}
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
