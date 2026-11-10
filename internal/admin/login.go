package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grodier/rss/internal/clientip"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// The login's errors are generic, so neither step says which emails are
// admins' or which part was wrong.
const (
	loginErrorMessage = "That email and password don't match an admin."
	codeErrorMessage  = "That code didn't work."
)

type loginPage struct {
	Nav   navData
	Form  loginForm
	Flash string
}

type loginForm struct {
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedAdmin(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	flash := s.sessionManager.PopString(r.Context(), sessionFlash)
	s.renderLogin(w, r, http.StatusOK, loginPage{Flash: flash})
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, data loginPage) {
	data.Nav = s.nav(r, "")
	if err := s.renderHTML(w, status, "login.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// loginFormHandler checks the email and password, the first step. When they
// match, the session holds the admin as pending, for the code step.
func (s *Server) loginFormHandler(w http.ResponseWriter, r *http.Request) {
	var form loginForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if ok, retryAfter := s.loginLimiter.AllowWithRetry(clientip.RateLimitKey(s.clientIP(r))); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, loginPage{Form: form})
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

	admin, err := s.services.Admins.Authenticate(r.Context(), form.Email, form.Password)
	if errors.Is(err, rss.ErrNoAccount) || errors.Is(err, rss.ErrWrongPassword) {
		s.auditLoginFailed(r, rss.AuditEntry{AdminEmail: form.Email})
		form.AddNonFieldError(loginErrorMessage)
		s.renderLogin(w, r, http.StatusUnprocessableEntity, loginPage{Form: form})
		return
	}
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// Against session fixation.
	if err := s.sessionManager.RenewToken(r.Context()); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.sessionManager.Put(r.Context(), sessionPendingID, admin.ID)
	s.sessionManager.Put(r.Context(), sessionPendingAt, s.now().Unix())
	http.Redirect(w, r, "/login/code", http.StatusSeeOther)
}

type codePage struct {
	Nav  navData
	Form codeForm
	// Confirming is true when a logged-in admin is asked for a code again,
	// before a destructive action (requireRecentCode).
	Confirming bool
}

type codeForm struct {
	Code string `form:"code"`
	// Next is where to go once the code works: a local path, else "".
	Next                string `form:"next"`
	validator.Validator `form:"-"`
}

// codeSubject returns the admin a code is for: the logged-in admin
// (confirming), or the one whose password just passed. ok is false if
// neither, or the pending login is older than pendingLoginWindow.
func (s *Server) codeSubject(r *http.Request) (adminID string, confirming, ok bool) {
	if a, ok := s.authenticatedAdmin(r); ok {
		return a.ID, true, true
	}
	id := s.sessionManager.GetString(r.Context(), sessionPendingID)
	at := time.Unix(s.sessionManager.GetInt64(r.Context(), sessionPendingAt), 0)
	if id == "" || s.now().Sub(at) > pendingLoginWindow {
		return "", false, false
	}
	return id, false, true
}

func (s *Server) loginCodeHandler(w http.ResponseWriter, r *http.Request) {
	_, confirming, ok := s.codeSubject(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	form := codeForm{Next: safeReturnPath(r.URL.Query().Get("next"), "")}
	s.renderCode(w, r, http.StatusOK, codePage{Form: form, Confirming: confirming})
}

func (s *Server) renderCode(w http.ResponseWriter, r *http.Request, status int, data codePage) {
	data.Nav = s.nav(r, "")
	if err := s.renderHTML(w, status, "login_code.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// loginCodeFormHandler checks the second step's code, a TOTP code or a
// recovery code. It logs a pending admin in, or records that a logged-in one
// confirmed it's them.
func (s *Server) loginCodeFormHandler(w http.ResponseWriter, r *http.Request) {
	var form codeForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	form.Next = safeReturnPath(form.Next, "")

	adminID, confirming, ok := s.codeSubject(r)
	if !ok {
		s.sessionManager.Put(r.Context(), sessionFlash, "That took too long. Log in again.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	page := codePage{Form: form, Confirming: confirming}

	if ok, retryAfter := s.loginLimiter.AllowWithRetry(clientip.RateLimitKey(s.clientIP(r))); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		page.Form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderCode(w, r, http.StatusTooManyRequests, page)
		return
	}

	page.Form.CheckField(validator.NotBlank(form.Code), "code", "Code is required")
	page.Form.CheckField(validator.MaxChars(form.Code, 32), "code", codeErrorMessage)
	if !page.Form.Valid() {
		s.renderCode(w, r, http.StatusUnprocessableEntity, page)
		return
	}

	// Counted before the check, so concurrent guesses can't all get past
	// the limit; refunded when the code works.
	if ok, retryAfter := s.codeFailureLimiter.AllowWithRetry(adminID); !ok {
		ratelimit.SetRetryAfter(w, retryAfter)
		page.Form.AddNonFieldError(ratelimit.TooManyAttemptsMessage(retryAfter))
		s.renderCode(w, r, http.StatusTooManyRequests, page)
		return
	}

	ok, err := s.checkCode(r, adminID, form.Code)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if !ok {
		s.auditLoginFailed(r, rss.AuditEntry{AdminID: adminID})
		page.Form.AddFieldError("code", codeErrorMessage)
		s.renderCode(w, r, http.StatusUnprocessableEntity, page)
		return
	}
	s.codeFailureLimiter.Refund(adminID)

	ctx := r.Context()
	next := form.Next
	if !confirming {
		// Recorded first: an admin isn't logged in without its record.
		if err := s.audit(r, rss.AuditEntry{AdminID: adminID, Action: "login"}); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		if err := s.sessionManager.RenewToken(ctx); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		s.sessionManager.Remove(ctx, sessionPendingID)
		s.sessionManager.Remove(ctx, sessionPendingAt)
		s.sessionManager.Put(ctx, sessionAdminID, adminID)
		returnTo := safeReturnPath(s.sessionManager.PopString(ctx, sessionReturnTo), "")
		if next == "" {
			next = returnTo
		}
	}
	s.sessionManager.Put(ctx, sessionCodeAt, s.now().Unix())
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// checkCode reports whether code is the admin's: a TOTP code for a step
// after the last one accepted, which it then records, or an unused recovery
// code, which it then deletes.
func (s *Server) checkCode(r *http.Request, adminID, code string) (bool, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if recovery, ok := normalizeRecoveryCode(code); ok {
		return s.services.Admins.UseRecoveryCode(r.Context(), adminID, token.Hash(recovery))
	}

	admin, err := s.services.Admins.Get(r.Context(), adminID)
	if errors.Is(err, rss.ErrNoRecord) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, err := Open(s.config.SecretKey, admin.TOTPSecret)
	if err != nil {
		return false, err
	}
	step, ok := Verify(secret, code, s.now(), admin.TOTPLastStep)
	if !ok {
		return false, nil
	}
	return s.services.Admins.AcceptTOTPStep(r.Context(), adminID, step)
}

// auditLoginFailed records a wrong password (e names the submitted email)
// or a wrong code (e names the admin). An error is logged, not shown: the
// attempt changed nothing, and its answer stays the same.
func (s *Server) auditLoginFailed(r *http.Request, e rss.AuditEntry) {
	e.Action = "login_failed"
	if err := s.audit(r, e); err != nil {
		s.logError(r, err)
	}
}

// logoutHandler ends the admin's session: a new token, without the admin.
// Logging out works even when it can't be recorded, which is logged.
func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, ok := s.authenticatedAdmin(r); ok {
		if err := s.audit(r, rss.AuditEntry{Action: "logout"}); err != nil {
			s.logError(r, err)
		}
	}
	if err := s.sessionManager.RenewToken(ctx); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	for _, key := range []string{sessionAdminID, sessionCodeAt, sessionPendingID, sessionPendingAt, sessionReturnTo} {
		s.sessionManager.Remove(ctx, key)
	}
	s.sessionManager.Put(ctx, sessionFlash, "You've logged out.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeReturnPath returns p if it's a local path, which can't send the
// browser to another site, else fallback.
func safeReturnPath(p, fallback string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return fallback
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return p
}
