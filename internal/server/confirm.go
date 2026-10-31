package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// confirmLinkTTL is how long a confirmation link works.
const confirmLinkTTL = 7 * 24 * time.Hour

// sendConfirmation emails user a link to confirm their address.
func (s *Server) sendConfirmation(ctx context.Context, user rss.User) error {
	plain, hash, err := token.New()
	if err != nil {
		return err
	}

	err = s.services.TokenService.Create(ctx, rss.Token{
		Hash:      hash,
		UserID:    user.ID,
		Purpose:   rss.TokenConfirmEmail,
		Email:     user.Email,
		ExpiresAt: time.Now().Add(confirmLinkTTL),
	})
	if err != nil {
		return err
	}

	return s.sendEmail(ctx, user.Email, "confirm_email", struct{ Name, URL string }{
		Name: user.Name,
		URL:  s.absoluteURL("/confirm-email?token=" + plain),
	})
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

// linkExpiredPage is link_expired.html's data, so each kind of email link
// can say what it is and offer its own way on.
type linkExpiredPage struct {
	Title string
	Text  string
	// Note, when set, is said before the action.
	Note string
	// Form, when set, is a POST button; Link, when set, a link.
	Form *linkExpiredAction
	Link *linkExpiredAction
}

type linkExpiredAction struct {
	URL   string
	Label string
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
	// The token is in the URL: keep it out of Referer headers and caches.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

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
	http.Redirect(w, r, safeReturnPath(form.ReturnTo, "/settings"), http.StatusSeeOther)
}
