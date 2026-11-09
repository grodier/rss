package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/clientip"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

type signupForm struct {
	// Invite is the invite's token, from /signup?invite=, carried in a
	// hidden field.
	Invite              string `form:"invite"`
	Name                string `form:"name"`
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) signupHandler(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	plain := r.URL.Query().Get("invite")
	if plain == "" {
		s.renderInviteUnusable(w, r, http.StatusOK, inviteMissing)
		return
	}
	noReferrerNoStore(w)
	inv, ok := s.signupInvite(w, r, plain, http.StatusGone)
	if !ok {
		return
	}
	data := signupPage{Form: signupForm{Invite: plain}}
	if inv.Email != "" {
		data.Form.Name, data.Form.Email, data.BoundEmail = inv.Name, inv.Email, true
	}
	s.renderSignup(w, r, http.StatusOK, data)
}

type signupPage struct {
	Form signupForm
	// BoundEmail locks the email field: the invite is for Form.Email only.
	BoundEmail bool
	// DuplicateEmail adds links to /login and to reset the password under
	// the email field's error.
	DuplicateEmail bool
}

// renderSignup renders signup.html with status.
func (s *Server) renderSignup(w http.ResponseWriter, r *http.Request, status int, data signupPage) {
	if err := s.renderHTML(w, status, "signup.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// inviteMissing is renderInviteUnusable's reason for a signup with no
// invite; the others are rss.Invite.Status's, or "" for an unknown token.
const inviteMissing = "missing"

// renderInviteUnusable renders the page that says why signup can't go on,
// with no form: reason is inviteMissing, an rss.Invite status other than
// rss.InvitePending, or "" for a token that isn't an invite's.
func (s *Server) renderInviteUnusable(w http.ResponseWriter, r *http.Request, status int, reason string) {
	var data linkExpiredPage
	switch reason {
	case inviteMissing:
		data = linkExpiredPage{
			Title: "Paper Round is invite-only for now",
			Link:  &linkExpiredAction{URL: "/", Label: "Join the waitlist"},
		}
	case rss.InviteExpired:
		data = linkExpiredPage{Title: "This invite has expired", Text: "Ask whoever invited you for a new one."}
	case rss.InviteRedeemed:
		data = linkExpiredPage{
			Title: "This invite has already been used",
			Link:  &linkExpiredAction{URL: "/login", Label: "Log in"},
		}
	default:
		data = linkExpiredPage{Title: "This invite is no longer valid"}
	}
	if err := s.renderHTML(w, status, "link_expired.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// signupInvite returns the invite with the token plain if it can be used.
// When it can't, or on an error, it has rendered why, with status, and
// returns false.
func (s *Server) signupInvite(w http.ResponseWriter, r *http.Request, plain string, status int) (rss.Invite, bool) {
	inv, err := s.services.InviteService.GetByToken(r.Context(), token.Hash(plain))
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.renderInviteUnusable(w, r, status, "")
			return rss.Invite{}, false
		}
		s.serverErrorHTML(w, r, err)
		return rss.Invite{}, false
	}
	if st := inv.Status(time.Now()); st != rss.InvitePending {
		s.renderInviteUnusable(w, r, status, st)
		return rss.Invite{}, false
	}
	return inv, true
}

func (s *Server) signupFormHandler(w http.ResponseWriter, r *http.Request) {
	noReferrerNoStore(w)
	var form signupForm
	err := s.decodePostForm(w, r, &form)
	if err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if ok, retryAfter := s.signupLimiter.AllowWithRetry(clientip.RateLimitKey(s.clientIP(r))); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderSignup(w, r, http.StatusTooManyRequests, signupPage{Form: form})
		return
	}

	if form.Invite == "" {
		s.renderInviteUnusable(w, r, http.StatusUnprocessableEntity, inviteMissing)
		return
	}
	inv, ok := s.signupInvite(w, r, form.Invite, http.StatusUnprocessableEntity)
	if !ok {
		return
	}
	// A bound invite is for its address only, whatever was submitted.
	bound := inv.Email != ""
	if bound {
		form.Email = inv.Email
	}

	form.Name = strings.TrimSpace(form.Name)
	form.CheckNewAccount(form.Name, form.Email, form.Password)

	if !form.Valid() {
		s.renderSignup(w, r, http.StatusUnprocessableEntity, signupPage{Form: form, BoundEmail: bound})
		return
	}

	id, err := s.services.UserService.CreateFromInvite(r.Context(), token.Hash(form.Invite), form.Name, form.Email, form.Password)
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrDuplicateEmail):
			form.AddFieldError("email", "There's already an account with this email.")
			s.renderSignup(w, r, http.StatusUnprocessableEntity, signupPage{Form: form, BoundEmail: bound, DuplicateEmail: true})
		case errors.Is(err, rss.ErrInviteUnusable):
			// It was usable a moment ago: most likely someone else signed
			// up with it in between.
			s.renderInviteUnusable(w, r, http.StatusUnprocessableEntity, rss.InviteRedeemed)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	if err := s.startSession(r, id); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// A bound invite's account starts confirmed: its address was confirmed
	// joining the waitlist. Otherwise the account works without a confirmed
	// address, and the notice offers Resend, so a failure here doesn't fail
	// signup.
	if !bound {
		if err := s.sendConfirmation(r.Context(), rss.User{ID: id, Name: form.Name, Email: form.Email}); err != nil {
			s.logError(r, err)
		}
	}

	http.Redirect(w, r, s.popReturnTo(r), http.StatusSeeOther)
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	flash := s.sessionManager.PopString(r.Context(), "flash")
	s.renderLogin(w, r, http.StatusOK, loginPage{Flash: flash})
}

type loginPage struct {
	Form  loginForm
	Flash string
	// RateLimited adds a link to reset the password under the alert.
	RateLimited bool
}

// renderLogin renders login.html with status.
func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, data loginPage) {
	if err := s.renderHTML(w, status, "login.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type loginForm struct {
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) loginFormHandler(w http.ResponseWriter, r *http.Request) {
	var form loginForm
	err := s.decodePostForm(w, r, &form)
	if err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if ok, retryAfter := s.loginLimiter.AllowWithRetry(clientip.RateLimitKey(s.clientIP(r))); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, loginPage{Form: form, RateLimited: true})
		return
	}

	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	form.CheckField(validator.NotBlank(form.Password), "password", "Password is required")
	form.CheckField(validator.MaxChars(form.Password, 256), "password", "Password is too long")

	if !form.Valid() {
		s.renderLogin(w, r, http.StatusUnprocessableEntity, loginPage{Form: form})
		return
	}

	// Failures are counted per email, to slow password guessing against an
	// account. Lowercased because users.email is citext. Counted before
	// Authenticate, so concurrent attempts can't all get past the limit;
	// refunded below unless the password was wrong.
	emailKey := strings.ToLower(form.Email)
	if ok, retryAfter := s.loginFailureLimiter.AllowWithRetry(emailKey); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, loginPage{Form: form, RateLimited: true})
		return
	}

	id, err := s.services.UserService.Authenticate(r.Context(), form.Email, form.Password)
	if !errors.Is(err, rss.ErrWrongPassword) {
		s.loginFailureLimiter.Refund(emailKey)
	}
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrNoAccount):
			form.AddFieldError("email", "There's no account with this email.")
			s.renderLogin(w, r, http.StatusUnprocessableEntity, loginPage{Form: form})
		case errors.Is(err, rss.ErrWrongPassword):
			form.AddFieldError("password", "That password isn't right.")
			s.renderLogin(w, r, http.StatusUnprocessableEntity, loginPage{Form: form})
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	if err := s.startSession(r, id); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	http.Redirect(w, r, s.popReturnTo(r), http.StatusSeeOther)
}

// popReturnTo returns the page requireAuthentication sent the user away
// from, if it's a local path, else "/", and forgets it.
func (s *Server) popReturnTo(r *http.Request) string {
	return safeReturnPath(s.sessionManager.PopString(r.Context(), "returnTo"), "/")
}

// startSession logs userID in on this device: it renews the session token
// (against session fixation), creates a user_sessions row, and keeps its ID
// in the session.
func (s *Server) startSession(r *http.Request, userID string) error {
	if err := s.sessionManager.RenewToken(r.Context()); err != nil {
		return err
	}

	id, err := s.services.SessionService.Create(r.Context(), userID, time.Now().Add(-sessionIdleTimeout))
	if err != nil {
		return err
	}

	s.sessionManager.Put(r.Context(), "sessionID", id)
	return nil
}

func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.authenticatedSession(r); ok {
		if err := s.services.SessionService.Delete(r.Context(), sess.ID); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	err := s.sessionManager.RenewToken(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Remove(r.Context(), "sessionID")
	s.sessionManager.Put(r.Context(), "flash", "You've been logged out successfully!")

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
