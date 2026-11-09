package admin

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

// Admin sessions are short (see "Admin" in docs/decisions.md): they end after
// 30 minutes idle and 12 hours at most, and a destructive action asks for a
// code again when the last one is older than recentCodeWindow.
const (
	sessionIdleTimeout = 30 * time.Minute
	sessionLifetime    = 12 * time.Hour
	recentCodeWindow   = 10 * time.Minute
	// pendingLoginWindow is how long after the password the code step may
	// follow.
	pendingLoginWindow = 5 * time.Minute
)

// The session's keys.
const (
	// sessionAdminID is the logged-in admin's ID, set once both steps pass.
	sessionAdminID = "adminID"
	// sessionCodeAt is when the admin last entered a code (Unix seconds).
	sessionCodeAt = "codeAt"
	// sessionPendingID and sessionPendingAt are the admin whose password
	// passed, and when (Unix seconds), until the code step.
	sessionPendingID = "pendingAdminID"
	sessionPendingAt = "pendingAt"
	// sessionReturnTo is the page requireAuthentication sent the admin away
	// from.
	sessionReturnTo = "returnTo"
	sessionFlash    = "flash"
)

// NewSessionManager returns the admin's session manager, storing sessions in
// store (postgresstore's admin_sessions table in production). In production
// the cookie is __Host-admin_session, which browsers only accept with Secure
// and Path=/ and without a Domain, so no other subdomain can set it; locally,
// over http, it's admin_session without Secure, like the reader's.
func NewSessionManager(store scs.Store, env string) *scs.SessionManager {
	sm := scs.New()
	sm.Store = store
	sm.IdleTimeout = sessionIdleTimeout
	sm.Lifetime = sessionLifetime
	sm.Cookie.Name = "admin_session"
	sm.Cookie.Path = "/"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteStrictMode
	if env == "production" {
		sm.Cookie.Name = "__Host-admin_session"
		sm.Cookie.Secure = true
	}
	return sm
}
