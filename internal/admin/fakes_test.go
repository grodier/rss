package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/password"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// testKey seals test servers' TOTP secrets.
var testKey = []byte("0123456789abcdef0123456789abcdef")

// fakeAdmins is an in-memory AdminStore with the repository's behavior.
type fakeAdmins struct {
	mu     sync.Mutex
	admins map[string]*fakeAdmin // by ID
	nextID int
}

type fakeAdmin struct {
	rss.Admin
	hashedPassword string
	codes          map[string]bool // recovery codes' hashes
}

func newFakeAdmins() *fakeAdmins {
	return &fakeAdmins{admins: map[string]*fakeAdmin{}}
}

func (f *fakeAdmins) byEmail(email string) *fakeAdmin {
	for _, a := range f.admins {
		if strings.EqualFold(a.Email, email) {
			return a
		}
	}
	return nil
}

func (f *fakeAdmins) Authenticate(_ context.Context, email, pw string) (rss.Admin, error) {
	f.mu.Lock()
	a := f.byEmail(email)
	f.mu.Unlock()
	if a == nil {
		return rss.Admin{}, rss.ErrNoAccount
	}
	ok, err := password.Verify(pw, a.hashedPassword)
	if err != nil {
		return rss.Admin{}, err
	}
	if !ok {
		return rss.Admin{}, rss.ErrWrongPassword
	}
	return f.Get(context.Background(), a.ID)
}

func (f *fakeAdmins) Get(_ context.Context, id string) (rss.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.admins[id]
	if !ok {
		return rss.Admin{}, rss.ErrNoRecord
	}
	return a.Admin, nil
}

func (f *fakeAdmins) GetByEmail(_ context.Context, email string) (rss.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byEmail(email)
	if a == nil {
		return rss.Admin{}, rss.ErrNoRecord
	}
	return a.Admin, nil
}

func (f *fakeAdmins) Create(_ context.Context, email string, c rss.AdminCredentials) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byEmail(email) != nil {
		return "", rss.ErrDuplicateEmail
	}
	f.nextID++
	id := fmt.Sprintf("admin-%d", f.nextID)
	a := &fakeAdmin{Admin: rss.Admin{ID: id, Email: email, CreatedAt: time.Now()}}
	a.set(c)
	f.admins[id] = a
	return id, nil
}

func (f *fakeAdmins) Reset(_ context.Context, email string, c rss.AdminCredentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byEmail(email)
	if a == nil {
		return rss.ErrNoRecord
	}
	a.set(c)
	return nil
}

func (a *fakeAdmin) set(c rss.AdminCredentials) {
	a.hashedPassword = string(c.HashedPassword)
	a.TOTPSecret = c.SealedTOTPSecret
	a.TOTPLastStep = c.TOTPLastStep
	a.codes = map[string]bool{}
	for _, h := range c.RecoveryCodeHashes {
		a.codes[string(h)] = true
	}
}

func (f *fakeAdmins) AcceptTOTPStep(_ context.Context, id string, step int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.admins[id]
	if !ok || a.TOTPLastStep >= step {
		return false, nil
	}
	a.TOTPLastStep = step
	return true, nil
}

func (f *fakeAdmins) UseRecoveryCode(_ context.Context, id string, hash []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.admins[id]
	if !ok || !a.codes[string(hash)] {
		return false, nil
	}
	delete(a.codes, string(hash))
	return true, nil
}

func (f *fakeAdmins) delete(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.admins, id)
}

// The test admin's credentials.
const (
	testEmail    = "admin@example.com"
	testPassword = "correct horse battery staple"
	testRecovery = "abcd-efgh"
)

var testSecret = []byte("12345678901234567890")

// testPasswordHash is testPassword's hash, computed once: Argon2id is slow.
var testPasswordHash = sync.OnceValue(func() string {
	h, err := password.Hash(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// testNow is test servers' clock, when a test sets it.
var testNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// newTestServerWithAdmin returns a server whose clock is a pointer to
// testNow, with the test admin enrolled: testPassword, testSecret sealed
// with testKey, and the recovery code testRecovery.
func newTestServerWithAdmin(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	admins := newFakeAdmins()
	sealed, err := Seal(testKey, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admins.Create(t.Context(), testEmail, rss.AdminCredentials{
		HashedPassword:     []byte(testPasswordHash()),
		SealedTOTPSecret:   sealed,
		RecoveryCodeHashes: [][]byte{token.Hash(testRecovery)},
	}); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWith(t, Services{Database: pinger{}, Admins: admins})
	now := testNow
	s.now = func() time.Time { return now }
	return s, &now
}

// currentCode is the test admin's TOTP code at s's time.
func currentCode(s *Server) string {
	return Code(testSecret, s.now().Unix()/totpStepSeconds)
}

// browser sends requests to a server's router as a browser would: same-origin
// posts, keeping the session cookie.
type browser struct {
	handler http.Handler
	cookie  *http.Cookie
}

func newBrowser(s *Server) *browser {
	return &browser{handler: s.router()}
}

func (b *browser) do(t *testing.T, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	rr := httptest.NewRecorder()
	b.handler.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "admin_session" || c.Name == "__Host-admin_session" {
			b.cookie = c
		}
	}
	return rr
}

func (b *browser) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return b.do(t, http.MethodGet, path, nil)
}

func (b *browser) post(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	return b.do(t, http.MethodPost, path, form)
}

// loggedIn returns a browser logged in to s (newTestServerWithAdmin's) as
// the test admin, with a code entered at s's time.
func loggedIn(t *testing.T, s *Server) *browser {
	t.Helper()
	b := newBrowser(s)
	if rr := b.post(t, "/login", url.Values{"email": {testEmail}, "password": {testPassword}}); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login/code" {
		t.Fatalf("POST /login = %d to %q, want 303 to /login/code:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body)
	}
	if rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /login/code = %d, want 303:\n%s", rr.Code, rr.Body)
	}
	return b
}
