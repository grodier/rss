package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
				if errors.Is(err, auth.ErrSessionNotFound) {
					s.unauthorizedResponse(w, r, "invalid or expired token")
					return
				}
				s.serverErrorResponse(w, r, err)
				return
			}

			if session.RevokedAt != nil {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			now := time.Now()

			if !now.Before(session.ExpiresAt) {
				s.unauthorizedResponse(w, r, "invalid or expired token")
				return
			}

			if now.Sub(session.LastActivityAt) >= 30*24*time.Hour {
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

func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := contextGetUserID(r); !ok {
			s.unauthorizedResponse(w, r, "authentication required")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) RequireUserMatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUserID, ok := contextGetUserID(r)
		if !ok {
			s.unauthorizedResponse(w, r, "authentication required")
			return
		}

		paramUserID, err := uuid.Parse(chi.URLParam(r, "userID"))
		if err != nil {
			s.badRequestResponse(w, r, errors.New("invalid user ID in URL"))
			return
		}

		if ctxUserID != paramUserID {
			s.forbiddenResponse(w, r, "FORBIDDEN", "you do not have permission to access this resource")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) RequireStepUp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := contextGetSession(r)
		if !ok {
			s.unauthorizedResponse(w, r, "authentication required")
			return
		}

		now := time.Now()

		if session.LastStepUpAt == nil || session.LastStepUpAt.After(now) || now.Sub(*session.LastStepUpAt) > 15*time.Minute {
			s.forbiddenResponse(w, r, "STEP_UP_REQUIRED", "step-up authentication required")
			return
		}

		next.ServeHTTP(w, r)
	})
}
