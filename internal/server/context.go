package server

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/grodier/rss/internal/auth"
)

type contextKey string

const (
	userIDKey    contextKey = "userID"
	accountIDKey contextKey = "accountID"
	sessionKey   contextKey = "session"
)

func contextSetUserID(r *http.Request, id uuid.UUID) *http.Request {
	ctx := context.WithValue(r.Context(), userIDKey, id)
	return r.WithContext(ctx)
}

func contextGetUserID(r *http.Request) (uuid.UUID, bool) {
	id, ok := r.Context().Value(userIDKey).(uuid.UUID)
	return id, ok
}

func contextSetAccountID(r *http.Request, id uuid.UUID) *http.Request {
	ctx := context.WithValue(r.Context(), accountIDKey, id)
	return r.WithContext(ctx)
}

func contextGetAccountID(r *http.Request) (uuid.UUID, bool) {
	id, ok := r.Context().Value(accountIDKey).(uuid.UUID)
	return id, ok
}

func contextSetSession(r *http.Request, s *auth.Session) *http.Request {
	ctx := context.WithValue(r.Context(), sessionKey, s)
	return r.WithContext(ctx)
}

func contextGetSession(r *http.Request) (*auth.Session, bool) {
	s, ok := r.Context().Value(sessionKey).(*auth.Session)
	return s, ok
}
