package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/grodier/rss/internal/clientip"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// waitlistSourceRX is what a ?ref= must be to be kept as an entry's source.
var waitlistSourceRX = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// waitlistSource returns ref if it's a valid source, else "".
func waitlistSource(ref string) string {
	if waitlistSourceRX.MatchString(ref) {
		return ref
	}
	return ""
}

type waitlistForm struct {
	Email string `form:"email"`
	Name  string `form:"name"`
	// Sites are the sites they'd like to follow, as typed: names or
	// addresses, for the seed list and to check discovery finds their feeds
	// before their wave is invited.
	Sites string `form:"sites"`
	// Source is the hosting page's ?ref=, carried in a hidden field.
	Source string `form:"source"`
	// Website is the honeypot: hidden from people, so only bots fill it in.
	Website             string `form:"website"`
	validator.Validator `form:"-"`
}

// waitlistFormData is what the waitlist-form partial shows. The page that
// hosts the partial embeds it in its own data.
type waitlistFormData struct {
	Form waitlistForm
	// AccountExists adds a link to /login under the email field's error.
	AccountExists bool
}

func newWaitlistFormData(form waitlistForm) waitlistFormData {
	return waitlistFormData{Form: form}
}

// homePage is the logged-out home page's data. It hosts the waitlist form.
type homePage struct {
	Flash string
	waitlistFormData
}

// renderWaitlistHost renders the page that hosts the waitlist form with
// data and status: the first visit, and the form's errors. Today that's the
// logged-out home page; whatever page takes the form renders it here.
func (s *Server) renderWaitlistHost(w http.ResponseWriter, r *http.Request, status int, data waitlistFormData) {
	page := homePage{waitlistFormData: data}
	if status == http.StatusOK {
		page.Flash = s.sessionManager.PopString(r.Context(), "flash")
	}
	if err := s.renderHTML(w, status, "home.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// sendWaitlistConfirmation emails e the links to confirm their place and to
// leave the list, with plain, the entry's link token. The token doesn't
// expire: it works until a new confirmation email replaces it or the entry
// is deleted, since every waitlist email (the invite too) carries the leave
// link.
func (s *Server) sendWaitlistConfirmation(ctx context.Context, e rss.WaitlistEntry, plain string) error {
	return s.sendEmail(ctx, e.Email, "waitlist_confirm", struct{ Name, ConfirmURL, LeaveURL string }{
		Name:       e.Name,
		ConfirmURL: s.absoluteURL("/waitlist/confirm?token=" + plain),
		LeaveURL:   s.absoluteURL("/waitlist/leave?token=" + plain),
	})
}

func (s *Server) waitlistJoinHandler(w http.ResponseWriter, r *http.Request) {
	// Members don't need a place; the form isn't shown to them.
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var form waitlistForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	// A bot filled in the honeypot: answer as if it worked, so it learns
	// nothing, and store and send nothing.
	if form.Website != "" {
		s.sessionManager.Put(r.Context(), "waitlistJoinedTo", form.Email)
		http.Redirect(w, r, "/waitlist/joined", http.StatusSeeOther)
		return
	}

	if ok, retryAfter := s.waitlistLimiter.AllowWithRetry(clientip.RateLimitKey(s.clientIP(r))); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderWaitlistHost(w, r, http.StatusTooManyRequests, newWaitlistFormData(form))
		return
	}

	form.Name = strings.TrimSpace(form.Name)
	form.Sites = strings.TrimSpace(form.Sites)
	form.Source = waitlistSource(form.Source)
	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	form.CheckName(form.Name)
	form.CheckField(validator.MaxChars(form.Sites, 500), "sites", "Keep this under 500 characters")
	if !form.Valid() {
		s.renderWaitlistHost(w, r, http.StatusUnprocessableEntity, newWaitlistFormData(form))
		return
	}

	_, err := s.services.UserService.GetByEmail(r.Context(), form.Email)
	switch {
	case err == nil:
		form.AddFieldError("email", "There's already an account with this email.")
		data := newWaitlistFormData(form)
		data.AccountExists = true
		s.renderWaitlistHost(w, r, http.StatusUnprocessableEntity, data)
		return
	case !errors.Is(err, rss.ErrNoRecord):
		s.serverErrorHTML(w, r, err)
		return
	}

	plain, hash, err := token.New()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	entry, err := s.services.WaitlistService.Join(r.Context(), rss.WaitlistEntry{
		Email:  form.Email,
		Name:   form.Name,
		Sites:  form.Sites,
		Source: form.Source,
	}, hash)
	if err != nil {
		if errors.Is(err, rss.ErrDuplicateEmail) {
			form.AddFieldError("email", "You're already on the waitlist.")
			s.renderWaitlistHost(w, r, http.StatusUnprocessableEntity, newWaitlistFormData(form))
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	// Without the email the entry can't be confirmed; joining again sends a
	// new one.
	if err := s.sendWaitlistConfirmation(r.Context(), entry, plain); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "waitlistJoinedTo", entry.Email)
	http.Redirect(w, r, "/waitlist/joined", http.StatusSeeOther)
}

func (s *Server) waitlistJoinedHandler(w http.ResponseWriter, r *http.Request) {
	email := s.sessionManager.GetString(r.Context(), "waitlistJoinedTo")
	if email == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := struct{ Email string }{Email: email}
	if err := s.renderHTML(w, http.StatusOK, "waitlist_joined.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// waitlistLinkExpired renders the expired-link page for a waitlist link: its
// token was replaced by a newer email's, or the entry has left the list.
func (s *Server) waitlistLinkExpired(w http.ResponseWriter, r *http.Request) {
	data := linkExpiredPage{
		Title: "This link has expired",
		Text:  "Only the link in the newest waitlist email works, and none work once you've left the list.",
		Link:  &linkExpiredAction{URL: "/", Label: "Join the waitlist"},
	}
	if err := s.renderHTML(w, http.StatusGone, "link_expired.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// waitlistLinkPage is the confirm and leave pages' data.
type waitlistLinkPage struct {
	Email string
	Token string
}

// waitlistEntryByToken returns the entry with the link token plain. When
// there's none, or on an error, it has responded and returns false.
func (s *Server) waitlistEntryByToken(w http.ResponseWriter, r *http.Request, plain string) (rss.WaitlistEntry, bool) {
	e, err := s.services.WaitlistService.GetByToken(r.Context(), token.Hash(plain))
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.waitlistLinkExpired(w, r)
			return rss.WaitlistEntry{}, false
		}
		s.serverErrorHTML(w, r, err)
		return rss.WaitlistEntry{}, false
	}
	return e, true
}

// noReferrerNoStore keeps an email link's token, in the URL or the page, out
// of Referer headers and caches.
func noReferrerNoStore(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

// waitlistLinkHandler returns the GET handler of a waitlist email link: it
// shows page, whose button posts the token back.
func (s *Server) waitlistLinkHandler(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noReferrerNoStore(w)
		plain := r.URL.Query().Get("token")
		e, ok := s.waitlistEntryByToken(w, r, plain)
		if !ok {
			return
		}
		data := waitlistLinkPage{Email: e.Email, Token: plain}
		if err := s.renderHTML(w, http.StatusOK, page, data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
	}
}

type waitlistTokenForm struct {
	Token string `form:"token"`
}

// waitlistActionHandler returns the POST handler of a waitlist email link:
// it calls act with the token's hash, then goes home with flash.
func (s *Server) waitlistActionHandler(act func(ctx context.Context, tokenHash []byte) error, flash string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noReferrerNoStore(w)
		var form waitlistTokenForm
		if err := s.decodePostForm(w, r, &form); err != nil {
			s.formErrorResponse(w, r, err)
			return
		}
		if err := act(r.Context(), token.Hash(form.Token)); err != nil {
			if errors.Is(err, rss.ErrNoRecord) {
				s.waitlistLinkExpired(w, r)
				return
			}
			s.serverErrorHTML(w, r, err)
			return
		}
		s.sessionManager.Put(r.Context(), "flash", flash)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) waitlistConfirmHandler(w http.ResponseWriter, r *http.Request) {
	s.waitlistLinkHandler("waitlist_confirm.html")(w, r)
}

func (s *Server) waitlistConfirmFormHandler(w http.ResponseWriter, r *http.Request) {
	s.waitlistActionHandler(s.services.WaitlistService.Confirm,
		"You're on the waitlist. We'll email you when there's a place for you.")(w, r)
}

func (s *Server) waitlistLeaveHandler(w http.ResponseWriter, r *http.Request) {
	s.waitlistLinkHandler("waitlist_leave.html")(w, r)
}

func (s *Server) waitlistLeaveFormHandler(w http.ResponseWriter, r *http.Request) {
	s.waitlistActionHandler(s.services.WaitlistService.Leave,
		"You've left the waitlist. We won't email you again.")(w, r)
}
