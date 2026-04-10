package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grodier/rss/internal/auth"
)

type mockAuthRepo struct {
	auth.Repository
	getSessionByTokenHashFn func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error)
	updateSessionActivityFn func(ctx context.Context, db auth.DBTX, id uuid.UUID) error
}

func (m *mockAuthRepo) GetSessionByTokenHash(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
	return m.getSessionByTokenHashFn(ctx, db, tokenHash)
}

func (m *mockAuthRepo) UpdateSessionActivity(ctx context.Context, db auth.DBTX, id uuid.UUID) error {
	if m.updateSessionActivityFn != nil {
		return m.updateSessionActivityFn(ctx, db, id)
	}
	return nil
}

func validSession() auth.Session {
	return auth.Session{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		AccountID:      uuid.New(),
		TokenHash:      "test-hash",
		CreatedAt:      time.Now().Add(-time.Hour),
		LastActivityAt: time.Now().Add(-time.Minute),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}
}

func assertUnauthorized(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if got["error_code"] != "UNAUTHENTICATED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "UNAUTHENTICATED")
	}
}

func TestAuthenticate_NoAuthorizationHeader(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	repo := &mockAuthRepo{}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := contextGetUserID(r); ok {
			t.Error("expected no userID in context")
		}
		if _, ok := contextGetAccountID(r); ok {
			t.Error("expected no accountID in context")
		}
		if _, ok := contextGetSession(r); ok {
			t.Error("expected no session in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestAuthenticate_MalformedHeader(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	repo := &mockAuthRepo{}

	tests := []struct {
		name   string
		header string
	}{
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"no space after bearer", "Bearertoken123"},
		{"empty bearer", "Bearer "},
		{"lowercase bearer", "bearer some-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if _, ok := contextGetUserID(r); ok {
					t.Error("expected no userID in context")
				}
				w.WriteHeader(http.StatusOK)
			}))

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", tt.header)
			handler.ServeHTTP(rr, req)

			if !called {
				t.Error("next handler was not called")
			}
			if rr.Code != http.StatusOK {
				t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
			}
		})
	}
}

func TestAuthenticate_ValidTokenActiveSession(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()
	rawToken := "test-raw-token"
	expectedHash := auth.HashToken(rawToken)

	activityUpdated := false
	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			if tokenHash != expectedHash {
				t.Errorf("token hash: got %q, want %q", tokenHash, expectedHash)
			}
			return session, nil
		},
		updateSessionActivityFn: func(ctx context.Context, db auth.DBTX, id uuid.UUID) error {
			if id != session.ID {
				t.Errorf("session ID: got %v, want %v", id, session.ID)
			}
			activityUpdated = true
			return nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true

		userID, ok := contextGetUserID(r)
		if !ok {
			t.Fatal("expected userID in context")
		}
		if userID != session.UserID {
			t.Errorf("userID: got %v, want %v", userID, session.UserID)
		}

		accountID, ok := contextGetAccountID(r)
		if !ok {
			t.Fatal("expected accountID in context")
		}
		if accountID != session.AccountID {
			t.Errorf("accountID: got %v, want %v", accountID, session.AccountID)
		}

		sess, ok := contextGetSession(r)
		if !ok {
			t.Fatal("expected session in context")
		}
		if sess.ID != session.ID {
			t.Errorf("session ID: got %v, want %v", sess.ID, session.ID)
		}

		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if !activityUpdated {
		t.Error("session activity was not updated")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestAuthenticate_RevokedSession(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()
	revokedAt := time.Now().Add(-time.Hour)
	session.RevokedAt = &revokedAt

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return session, nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called for revoked session")
	}
	assertUnauthorized(t, rr)
}

func TestAuthenticate_ExpiredSession(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()
	session.ExpiresAt = time.Now().Add(-time.Hour)

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return session, nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called for expired session")
	}
	assertUnauthorized(t, rr)
}

func TestAuthenticate_IdleTimedOutSession(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()
	session.LastActivityAt = time.Now().Add(-31 * 24 * time.Hour)

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return session, nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called for idle-timed-out session")
	}
	assertUnauthorized(t, rr)
}

func TestAuthenticate_TokenNotFoundInDB(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return auth.Session{}, auth.ErrSessionNotFound
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer nonexistent-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called when token is not found")
	}
	assertUnauthorized(t, rr)
}

func TestAuthenticate_UnexpectedDBError(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return auth.Session{}, errors.New("connection refused")
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called on DB error")
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if got["error_code"] != "INTERNAL_ERROR" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "INTERNAL_ERROR")
	}
}

func TestAuthenticate_ExactExpiryBoundary(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	now := time.Now()
	session := validSession()
	session.ExpiresAt = now

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return session, nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called when now == expiresAt")
	}
	assertUnauthorized(t, rr)
}

func TestAuthenticate_ExactIdleBoundary(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()
	session.LastActivityAt = time.Now().Add(-30 * 24 * time.Hour)

	repo := &mockAuthRepo{
		getSessionByTokenHashFn: func(ctx context.Context, db auth.DBTX, tokenHash string) (auth.Session, error) {
			return session, nil
		},
	}

	called := false
	handler := s.Authenticate(repo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called when idle time is exactly 30 days")
	}
	assertUnauthorized(t, rr)
}

// RequireAuth tests

func TestRequireAuth_UserInContext(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	called := false
	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = contextSetUserID(req, uuid.New())
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireAuth_NoUserInContext(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	called := false
	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called without user in context")
	}
	assertUnauthorized(t, rr)
}

// RequireUserMatch tests

func requireUserMatchWithChi(s *Server, handler http.Handler, userIDParam string) (*http.Request, http.Handler) {
	r := chi.NewRouter()
	r.Route("/users/{userID}", func(r chi.Router) {
		r.Use(s.RequireUserMatch)
		r.Get("/", handler.ServeHTTP)
	})
	req := httptest.NewRequest(http.MethodGet, "/users/"+userIDParam, nil)
	return req, r
}

func TestRequireUserMatch_Matches(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	userID := uuid.New()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req, router := requireUserMatchWithChi(s, inner, userID.String())

	rr := httptest.NewRecorder()
	req = contextSetUserID(req, userID)
	router.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireUserMatch_Mismatch(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	ctxUserID := uuid.New()
	paramUserID := uuid.New()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req, router := requireUserMatchWithChi(s, inner, paramUserID.String())

	rr := httptest.NewRecorder()
	req = contextSetUserID(req, ctxUserID)
	router.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called on user mismatch")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if got["error_code"] != "FORBIDDEN" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "FORBIDDEN")
	}
}

func TestRequireUserMatch_InvalidUUID(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req, router := requireUserMatchWithChi(s, inner, "not-a-uuid")

	rr := httptest.NewRecorder()
	req = contextSetUserID(req, uuid.New())
	router.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called with invalid UUID")
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if got["error_code"] != "BAD_REQUEST" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "BAD_REQUEST")
	}
}

func TestRequireUserMatch_NoUserInContext(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	paramUserID := uuid.New()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req, router := requireUserMatchWithChi(s, inner, paramUserID.String())

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called without user in context")
	}
	assertUnauthorized(t, rr)
}

// RequireStepUp tests

func TestRequireStepUp_ValidStepUp(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	stepUpAt := time.Now().Add(-5 * time.Minute)
	session := validSession()
	session.LastStepUpAt = &stepUpAt

	called := false
	handler := s.RequireStepUp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = contextSetSession(req, &session)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireStepUp_Expired(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	stepUpAt := time.Now().Add(-20 * time.Minute)
	session := validSession()
	session.LastStepUpAt = &stepUpAt

	called := false
	handler := s.RequireStepUp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = contextSetSession(req, &session)
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called with expired step-up")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if got["error_code"] != "STEP_UP_REQUIRED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "STEP_UP_REQUIRED")
	}
}

func TestRequireStepUp_NullLastStepUpAt(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	session := validSession()

	called := false
	handler := s.RequireStepUp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = contextSetSession(req, &session)
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called with null last_step_up_at")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if got["error_code"] != "STEP_UP_REQUIRED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "STEP_UP_REQUIRED")
	}
}

func TestRequireStepUp_NoSessionInContext(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	called := false
	handler := s.RequireStepUp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler should not be called without session in context")
	}
	assertUnauthorized(t, rr)
}
