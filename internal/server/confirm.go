package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// sendConfirmation emails user a link to confirm their address.
func (s *Server) sendConfirmation(ctx context.Context, user rss.User) error {
	link, err := s.accountEmails.Confirmation(user, time.Now())
	if err != nil {
		return err
	}
	return s.sendLink(ctx, link)
}

// confirmNotice is what the confirm-notice partial shows: the address to
// confirm, and the page Resend returns to.
type confirmNotice struct {
	Email    string
	ReturnTo string
}

// confirmNotice returns the notice for a logged-in user who hasn't confirmed
// their address yet, else nil.
func (s *Server) confirmNotice(r *http.Request) *confirmNotice {
	sess, ok := s.authenticatedSession(r)
	if !ok || !sess.User.EmailConfirmedAt.IsZero() {
		return nil
	}
	return &confirmNotice{Email: sess.User.Email, ReturnTo: r.URL.RequestURI()}
}

// confirmLinkExpired renders the expired-link page for a confirmation link,
// with the way on that fits who's asking.
func (s *Server) confirmLinkExpired(w http.ResponseWriter, r *http.Request) {
	data := linkExpiredPage{
		Title: "This link has expired",
		Text:  "Confirmation links work for 7 days and only once.",
	}
	switch sess, ok := s.authenticatedSession(r); {
	case !ok:
		data.Link = &linkExpiredAction{URL: "/login", Label: "Log in to get a new link"}
	case sess.User.EmailConfirmedAt.IsZero():
		data.Form = &linkExpiredAction{URL: "/confirm-email/resend", Label: "Send a new link"}
	default:
		data.Note = "Your email address is already confirmed."
		data.Link = &linkExpiredAction{URL: "/settings", Label: "Go to Settings"}
	}

	if err := s.renderHTML(w, http.StatusGone, "link_expired.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) confirmEmailHandler(w http.ResponseWriter, r *http.Request) {
	plain := r.URL.Query().Get("token")
	t, err := s.services.TokenService.Get(r.Context(), token.Hash(plain), rss.TokenConfirmEmail)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.confirmLinkExpired(w, r)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Email string
		Token string
	}{Email: t.Email, Token: plain}
	if err := s.renderHTML(w, http.StatusOK, "confirm_email.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type confirmEmailForm struct {
	Token string `form:"token"`
}

func (s *Server) confirmEmailFormHandler(w http.ResponseWriter, r *http.Request) {
	var form confirmEmailForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	userID, err := s.services.UserService.ConfirmEmail(r.Context(), token.Hash(form.Token))
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.confirmLinkExpired(w, r)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	// Logged in as the confirmed account: Home, with a flash about it.
	// Anyone else (logged out, or another account) gets a page that says
	// it's done; it never logs anyone in.
	if sess, ok := s.authenticatedSession(r); ok && sess.User.ID == userID {
		s.sessionManager.Put(r.Context(), "flash", "Thanks, your email address is confirmed.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/confirm-email/done", http.StatusSeeOther)
}

// confirmEmailDoneHandler says an address is confirmed. It's static, with no
// token or account in it, so it shows the same text to anyone who opens it,
// and a reload doesn't re-post a used token.
func (s *Server) confirmEmailDoneHandler(w http.ResponseWriter, r *http.Request) {
	data := struct{ LoggedIn bool }{LoggedIn: s.isAuthenticated(r)}
	if err := s.renderHTML(w, http.StatusOK, "confirm_email_done.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type confirmResendForm struct {
	ReturnTo string `form:"return_to"`
}

func (s *Server) confirmResendHandler(w http.ResponseWriter, r *http.Request) {
	var form confirmResendForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	sess, _ := s.authenticatedSession(r)
	user := sess.User
	var flash string
	switch {
	case !user.EmailConfirmedAt.IsZero():
		flash = "Your email address is already confirmed."
	case !s.confirmResendLimiter.Allow(user.ID):
		flash = "We've already sent you several links. Check your spam folder, or try again in an hour."
	default:
		if err := s.sendConfirmation(r.Context(), user); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		flash = "We sent a new link to " + user.Email + "."
	}

	s.sessionManager.Put(r.Context(), "flash", flash)
	http.Redirect(w, r, httpserve.SafeReturnPath(form.ReturnTo, "/settings"), http.StatusSeeOther)
}
