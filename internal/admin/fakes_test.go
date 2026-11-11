package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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
	// actors holds who Create and Reset were called by, in order.
	actors []rss.AuditActor
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

func (f *fakeAdmins) Create(_ context.Context, email string, c rss.AdminCredentials, by rss.AuditActor) (string, error) {
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
	f.actors = append(f.actors, by)
	return id, nil
}

func (f *fakeAdmins) Reset(_ context.Context, email string, c rss.AdminCredentials, by rss.AuditActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byEmail(email)
	if a == nil {
		return rss.ErrNoRecord
	}
	a.set(c)
	f.actors = append(f.actors, by)
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

// fakeAudit is an in-memory AuditStore with the repository's behavior,
// whose Record returns err when it's set.
type fakeAudit struct {
	mu     sync.Mutex
	events []rss.AuditEvent
	err    error
}

func (f *fakeAudit) Record(_ context.Context, e rss.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, rss.AuditEvent{ID: int64(len(f.events) + 1), OccurredAt: time.Now(), AuditEntry: e})
	return nil
}

func (f *fakeAudit) List(_ context.Context, filter rss.AuditFilter, limit int) ([]rss.AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rss.AuditEvent
	for i := len(f.events) - 1; i >= 0 && len(out) < limit; i-- {
		e := f.events[i]
		if (filter.Action != "" && e.Action != filter.Action) ||
			(filter.TargetType != "" && e.TargetType != filter.TargetType) ||
			(filter.TargetID != "" && e.TargetID != filter.TargetID) ||
			(filter.BeforeID > 0 && e.ID >= filter.BeforeID) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
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
	return newTestServerWithAdminConfig(t, Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}, Services{Database: pinger{}})
}

// newTestServerWithAdminConfig is newTestServerWithAdmin with cfg and
// services, whose AdminStore it replaces.
func newTestServerWithAdminConfig(t *testing.T, cfg Config, services Services) (*Server, *time.Time) {
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
	}, rss.AuditActor{}); err != nil {
		t.Fatal(err)
	}
	services.Admins = admins
	s := newTestServerConfig(t, cfg, services)
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

// fakeWaitlist is an in-memory WaitlistStore: its rows, oldest first, are
// on a tab by their confirmation and their latest invite's status at
// testNow, as the repository puts them.
type fakeWaitlist struct {
	mu   sync.Mutex
	rows []rss.WaitlistRow
}

func waitlistTabOf(r rss.WaitlistRow) string {
	switch {
	case r.ConfirmedAt.IsZero():
		return rss.WaitlistUnconfirmed
	case r.Invite.ID != "" && r.Invite.Status(testNow) == rss.InvitePending:
		return rss.WaitlistInvited
	}
	return rss.WaitlistWaiting
}

func (f *fakeWaitlist) Counts(context.Context) (rss.WaitlistCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var c rss.WaitlistCounts
	for _, r := range f.rows {
		switch waitlistTabOf(r) {
		case rss.WaitlistWaiting:
			c.Waiting++
		case rss.WaitlistInvited:
			c.Invited++
		default:
			c.Unconfirmed++
		}
	}
	return c, nil
}

func (f *fakeWaitlist) List(_ context.Context, tab string, limit, offset int) ([]rss.WaitlistRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rss.WaitlistRow
	for _, r := range f.rows {
		if waitlistTabOf(r) == tab {
			out = append(out, r)
		}
	}
	out = out[min(offset, len(out)):]
	return out[:min(limit, len(out))], nil
}

func (f *fakeWaitlist) ListWaiting(_ context.Context, ids []string) ([]rss.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rss.WaitlistEntry
	for _, r := range f.rows {
		if waitlistTabOf(r) == rss.WaitlistWaiting && slices.Contains(ids, r.ID) {
			out = append(out, r.WaitlistEntry)
		}
	}
	return out, nil
}

func (f *fakeWaitlist) GetByEmail(_ context.Context, email string) (rss.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if strings.EqualFold(r.Email, email) {
			return r.WaitlistEntry, nil
		}
	}
	return rss.WaitlistEntry{}, rss.ErrNoRecord
}

func (f *fakeWaitlist) Oldest(_ context.Context, n int) ([]rss.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rss.WaitlistEntry
	for _, r := range f.rows {
		if waitlistTabOf(r) == rss.WaitlistWaiting && len(out) < n {
			out = append(out, r.WaitlistEntry)
		}
	}
	return out, nil
}

// fakeInvites is an in-memory InviteStore: invites, newest first, and the
// waves' summaries, with what each write was given. sendErr, resendErr and
// revokeErr, when set, are what SendWave and SendInvite, Resend and Revoke
// return instead of writing.
type fakeInvites struct {
	mu      sync.Mutex
	invites []rss.Invite
	waveSum []rss.InviteWave

	waves   []rss.Wave       // sent by SendWave
	sent    []rss.WaveInvite // sent by SendInvite
	created []rss.Invite     // made by Create
	resent  []string         // IDs given to Resend
	revoked []string         // IDs revoked
	actors  []rss.AuditActor // by each write, in order
	// available is what the last send's available returned.
	available int
	usage     rss.EmailUsage

	sendErr, resendErr, revokeErr error
}

func (f *fakeInvites) SendWave(_ context.Context, w rss.Wave, available func(rss.EmailUsage) int, by rss.AuditActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.available = available(f.usage)
	if f.sendErr != nil {
		return f.sendErr
	}
	f.waves = append(f.waves, w)
	f.actors = append(f.actors, by)
	return nil
}

func (f *fakeInvites) SendInvite(_ context.Context, inv rss.WaveInvite, note string, expiresAt time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.available = available(f.usage)
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.sent = append(f.sent, inv)
	f.created = append(f.created, rss.Invite{Email: inv.Email, WaitlistID: inv.WaitlistID, Note: note, ExpiresAt: expiresAt})
	f.actors = append(f.actors, by)
	return "sent-invite", nil
}

func (f *fakeInvites) Create(_ context.Context, inv rss.Invite, _ []byte, by rss.AuditActor) (rss.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv.ID = fmt.Sprintf("created-%d", len(f.created)+1)
	f.created = append(f.created, inv)
	f.actors = append(f.actors, by)
	return inv, nil
}

func (f *fakeInvites) Get(_ context.Context, id string) (rss.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inv := range f.invites {
		if inv.ID == id {
			return inv, nil
		}
	}
	return rss.Invite{}, rss.ErrNoRecord
}

func (f *fakeInvites) List(_ context.Context, before string, limit int) ([]rss.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	start := 0
	if before != "" {
		start = slices.IndexFunc(f.invites, func(i rss.Invite) bool { return i.ID == before }) + 1
		if start == 0 {
			return nil, nil
		}
	}
	out := f.invites[start:]
	return out[:min(limit, len(out))], nil
}

func (f *fakeInvites) Waves(context.Context) ([]rss.InviteWave, error) { return f.waveSum, nil }

func (f *fakeInvites) Revoke(_ context.Context, id string, by rss.AuditActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked = append(f.revoked, id)
	f.actors = append(f.actors, by)
	return nil
}

func (f *fakeInvites) Resend(_ context.Context, id string, inv rss.WaveInvite, _ time.Time, available func(rss.EmailUsage) int, by rss.AuditActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.available = available(f.usage)
	if f.resendErr != nil {
		return f.resendErr
	}
	f.resent = append(f.resent, id)
	f.sent = append(f.sent, inv)
	f.actors = append(f.actors, by)
	return nil
}

// fakeEmails is an EmailStore whose usage is fixed.
type fakeEmails struct{ usage rss.EmailUsage }

func (f *fakeEmails) Usage(context.Context, time.Time) (rss.EmailUsage, error) { return f.usage, nil }
