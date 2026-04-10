package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/auth"
)

func (s *Server) Authenticate(repo auth.Repository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !found || token == "" {
				next.ServeHTTP(w, r)
				return
			}

			hash := auth.HashToken(token)

			session, err := repo.GetSessionByTokenHash(r.Context(), s.DB, hash)
			if err != nil {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			if session.RevokedAt != nil {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			now := time.Now()

			if now.After(session.ExpiresAt) {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			if now.Sub(session.LastActivityAt) > 30*24*time.Hour {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			if err := repo.UpdateSessionActivity(r.Context(), s.DB, session.ID); err != nil {
				s.logger.Error("failed to update session activity", "session_id", session.ID, "error", err)
			}

			r = contextSetUserID(r, session.UserID)
			r = contextSetAccountID(r, session.AccountID)
			r = contextSetSession(r, &session)

			next.ServeHTTP(w, r)
		})
	}
}
