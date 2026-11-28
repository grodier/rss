package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

const (
	testInboundSecret = "inbound-test-secret"
	testInboundDomain = "in.localhost"
)

// fakeInboundStore knows the addresses in addresses (local part → address
// ID and user ID), refuses those in noAccess, and keeps what Store stores.
type fakeInboundStore struct {
	addresses map[string][2]string
	noAccess  map[string]bool
	storeErr  error

	mu     sync.Mutex
	stored [][]byte
	seen   map[string]bool // address ID + raw, for replays
	// resolved is the local parts ResolveAddress was asked for.
	resolved []string
}

func (f *fakeInboundStore) ResolveAddress(_ context.Context, localPart string) (string, string, error) {
	f.mu.Lock()
	f.resolved = append(f.resolved, localPart)
	f.mu.Unlock()
	if f.noAccess[localPart] {
		return "", "", rss.ErrNoAccess
	}
	a, ok := f.addresses[localPart]
	if !ok {
		return "", "", rss.ErrNoRecord
	}
	return a[0], a[1], nil
}

func (f *fakeInboundStore) Store(_ context.Context, addressID, _ string, raw []byte) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	key := addressID + "\x00" + string(raw)
	if f.seen[key] {
		return rss.ErrDuplicateInbound
	}
	f.seen[key] = true
	f.stored = append(f.stored, bytes.Clone(raw))
	return nil
}

// fakeJobObserver keeps the outcomes observed, by runner.
type fakeJobObserver struct {
	mu       sync.Mutex
	outcomes []string // runner + " " + outcome
}

func (f *fakeJobObserver) Observe(runner, outcome string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes = append(f.outcomes, runner+" "+outcome)
}

func (f *fakeJobObserver) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.outcomes) == 0 {
		return ""
	}
	return f.outcomes[len(f.outcomes)-1]
}

func newInboundServer(t *testing.T, store *fakeInboundStore) (*Server, *fakeJobObserver) {
	t.Helper()
	jobs := &fakeJobObserver{}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", NewsletterDomain: testInboundDomain, InboundSecret: testInboundSecret},
		Services{InboundService: store, JobMetrics: jobs})
	return s, jobs
}

// inboundRequest is POST /inbound/email for to with body, signed with secret
// at ts.
func inboundRequest(t *testing.T, secret string, ts time.Time, to string, body io.Reader, signed []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/inbound/email", body)
	sec := strconv.FormatInt(ts.Unix(), 10)
	req.Header.Set("X-Inbound-To", to)
	req.Header.Set("X-Inbound-Signature", "t="+sec+",v1="+hex.EncodeToString(inboundMAC(secret, sec, to, signed)))
	return req
}

// postInbound posts raw to to through the router, signed now with the
// right secret.
func postInbound(t *testing.T, s *Server, to string, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, inboundRequest(t, testInboundSecret, time.Now(), to, bytes.NewReader(raw), raw))
	return rr
}

func knownInboundStore() *fakeInboundStore {
	return &fakeInboundStore{
		addresses: map[string][2]string{
			"george.k7x2m9": {"address-1", "user-1"},
			"retired.abc":   {"address-2", "user-1"},
		},
		noAccess: map[string]bool{"lapsed.xyz": true},
	}
}

const testEmail = "From: news@example.com\r\nSubject: Hi\r\n\r\nHello\r\n"

// errReader fails every read, and says whether anything read it.
type errReader struct{ read bool }

func (e *errReader) Read([]byte) (int, error) {
	e.read = true
	return 0, errors.New("read the body")
}

func TestInboundEmailSignature(t *testing.T) {
	raw := []byte(testEmail)
	to := "george.k7x2m9@" + testInboundDomain

	t.Run("valid", func(t *testing.T) {
		store := knownInboundStore()
		s, jobs := newInboundServer(t, store)
		rr := postInbound(t, s, to, raw)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rr.Code)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want none", rr.Body.String())
		}
		if got := jobs.last(); got != "inbound accepted" {
			t.Errorf("outcome = %q, want inbound accepted", got)
		}
	})

	tests := []struct {
		name string
		req  func(t *testing.T) *http.Request
	}{
		{"the wrong secret", func(t *testing.T) *http.Request {
			return inboundRequest(t, "another-secret", time.Now(), to, bytes.NewReader(raw), raw)
		}},
		{"a changed body", func(t *testing.T) *http.Request {
			return inboundRequest(t, testInboundSecret, time.Now(), to, strings.NewReader(testEmail+"P.S."), raw)
		}},
		{"a changed X-Inbound-To", func(t *testing.T) *http.Request {
			req := inboundRequest(t, testInboundSecret, time.Now(), to, bytes.NewReader(raw), raw)
			req.Header.Set("X-Inbound-To", "retired.abc@"+testInboundDomain)
			return req
		}},
		{"no signature", func(t *testing.T) *http.Request {
			req := inboundRequest(t, testInboundSecret, time.Now(), to, bytes.NewReader(raw), raw)
			req.Header.Del("X-Inbound-Signature")
			return req
		}},
		{"no recipient", func(t *testing.T) *http.Request {
			req := inboundRequest(t, testInboundSecret, time.Now(), to, bytes.NewReader(raw), raw)
			req.Header.Del("X-Inbound-To")
			return req
		}},
		{"a MAC that isn't hex", func(t *testing.T) *http.Request {
			req := inboundRequest(t, testInboundSecret, time.Now(), to, bytes.NewReader(raw), raw)
			req.Header.Set("X-Inbound-Signature", "t="+strconv.FormatInt(time.Now().Unix(), 10)+",v1=zz")
			return req
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := knownInboundStore()
			s, jobs := newInboundServer(t, store)
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, tt.req(t))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}
			if got := jobs.last(); got != "inbound bad_signature" {
				t.Errorf("outcome = %q, want inbound bad_signature", got)
			}
			if len(store.stored) != 0 || len(store.resolved) != 0 {
				t.Errorf("stored %d and resolved %v, want nothing", len(store.stored), store.resolved)
			}
		})
	}

	for _, off := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		t.Run("t "+off.String()+" off, before reading the body", func(t *testing.T) {
			s, jobs := newInboundServer(t, knownInboundStore())
			body := &errReader{}
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, inboundRequest(t, testInboundSecret, time.Now().Add(off), to, body, nil))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}
			if body.read {
				t.Error("the body was read")
			}
			if got := jobs.last(); got != "inbound bad_signature" {
				t.Errorf("outcome = %q, want inbound bad_signature", got)
			}
		})
	}

	t.Run("t 4 minutes off", func(t *testing.T) {
		s, _ := newInboundServer(t, knownInboundStore())
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, inboundRequest(t, testInboundSecret, time.Now().Add(-4*time.Minute), to, bytes.NewReader(raw), raw))
		if rr.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202", rr.Code)
		}
	})
}

func TestInboundEmailRecipient(t *testing.T) {
	tests := []struct {
		name, to    string
		want        int
		wantLocal   string // the local part looked up; "" for none
		wantOutcome string
	}{
		{"upper case", "George.K7X2M9@IN.localhost", http.StatusAccepted, "george.k7x2m9", "inbound accepted"},
		{"a +tag", "george.k7x2m9+news@in.localhost", http.StatusAccepted, "george.k7x2m9", "inbound accepted"},
		{"another domain", "george.k7x2m9@example.com", http.StatusNotFound, "", "inbound unknown_address"},
		{"a subdomain of another domain", "george.k7x2m9@evil-in.localhost", http.StatusNotFound, "", "inbound unknown_address"},
		{"no local part", "+news@in.localhost", http.StatusNotFound, "", "inbound unknown_address"},
		{"unknown", "nobody.123@in.localhost", http.StatusNotFound, "nobody.123", "inbound unknown_address"},
		{"no access", "lapsed.xyz@in.localhost", http.StatusNotFound, "lapsed.xyz", "inbound no_access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := knownInboundStore()
			s, jobs := newInboundServer(t, store)
			rr := postInbound(t, s, tt.to, []byte(testEmail))
			if rr.Code != tt.want {
				t.Errorf("status = %d, want %d", rr.Code, tt.want)
			}
			var looked string
			if len(store.resolved) > 0 {
				looked = store.resolved[0]
			}
			if looked != tt.wantLocal {
				t.Errorf("looked up %q, want %q", looked, tt.wantLocal)
			}
			if got := jobs.last(); got != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", got, tt.wantOutcome)
			}
		})
	}
}

func TestInboundEmailOff(t *testing.T) {
	tests := []struct{ name, domain, secret string }{
		{"no newsletter domain", "", testInboundSecret},
		{"no inbound secret", testInboundDomain, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := knownInboundStore()
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", NewsletterDomain: tt.domain, InboundSecret: tt.secret},
				Services{InboundService: store})
			rr := postInbound(t, s, "george.k7x2m9@"+testInboundDomain, []byte(testEmail))
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if len(store.resolved) != 0 {
				t.Errorf("resolved %v, want nothing", store.resolved)
			}
		})
	}
}

func TestInboundEmailStores(t *testing.T) {
	store := knownInboundStore()
	s, jobs := newInboundServer(t, store)
	to := "george.k7x2m9@" + testInboundDomain
	raw := []byte(testEmail + "\x00\xff binary is kept as is")

	if rr := postInbound(t, s, to, raw); rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	if len(store.stored) != 1 || !bytes.Equal(store.stored[0], raw) {
		t.Fatalf("stored %q, want exactly the body", store.stored)
	}

	// A replay while the first copy waits: accepted, nothing more stored.
	if rr := postInbound(t, s, to, raw); rr.Code != http.StatusAccepted {
		t.Errorf("replay: status = %d, want 202", rr.Code)
	}
	if len(store.stored) != 1 {
		t.Errorf("replay: stored %d emails, want 1", len(store.stored))
	}
	if got := jobs.last(); got != "inbound accepted" {
		t.Errorf("replay: outcome = %q, want inbound accepted", got)
	}
}

func TestInboundEmailTooBig(t *testing.T) {
	store := knownInboundStore()
	s, jobs := newInboundServer(t, store)
	raw := bytes.Repeat([]byte("a"), rss.InboundMaxBytes+1)
	rr := postInbound(t, s, "george.k7x2m9@"+testInboundDomain, raw)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
	if got := jobs.last(); got != "inbound too_big" {
		t.Errorf("outcome = %q, want inbound too_big", got)
	}
	if len(store.stored) != 0 {
		t.Errorf("stored %d emails, want none", len(store.stored))
	}

	// Exactly the limit is accepted.
	if rr := postInbound(t, s, "george.k7x2m9@"+testInboundDomain, raw[:rss.InboundMaxBytes]); rr.Code != http.StatusAccepted {
		t.Errorf("at the limit: status = %d, want 202", rr.Code)
	}
}

func TestInboundEmailLimits(t *testing.T) {
	// email makes each email different, so none is a replay.
	email := func(i int) []byte { return []byte(testEmail + strconv.Itoa(i)) }

	t.Run("per address", func(t *testing.T) {
		s, jobs := newInboundServer(t, knownInboundStore())
		to := "george.k7x2m9@" + testInboundDomain
		for i := range 100 {
			if rr := postInbound(t, s, to, email(i)); rr.Code != http.StatusAccepted {
				t.Fatalf("email %d: status = %d, want 202", i, rr.Code)
			}
		}
		rr := postInbound(t, s, to, email(100))
		if rr.Code != http.StatusTooManyRequests {
			t.Errorf("email 101: status = %d, want 429", rr.Code)
		}
		if rr.Header().Get("Retry-After") == "" {
			t.Error("no Retry-After")
		}
		if got := jobs.last(); got != "inbound rate_limited" {
			t.Errorf("outcome = %q, want inbound rate_limited", got)
		}
		// The reader's other address still receives.
		if rr := postInbound(t, s, "retired.abc@"+testInboundDomain, email(0)); rr.Code != http.StatusAccepted {
			t.Errorf("another address: status = %d, want 202", rr.Code)
		}
	})

	t.Run("per reader", func(t *testing.T) {
		store := &fakeInboundStore{addresses: map[string][2]string{
			"a.1": {"address-a", "user-1"}, "b.1": {"address-b", "user-1"},
			"c.1": {"address-c", "user-1"}, "d.1": {"address-d", "user-1"},
			"other.1": {"address-o", "user-2"},
		}}
		s, _ := newInboundServer(t, store)
		for i := range 300 {
			to := []string{"a.1", "b.1", "c.1", "d.1"}[i%4] + "@" + testInboundDomain
			if rr := postInbound(t, s, to, email(i)); rr.Code != http.StatusAccepted {
				t.Fatalf("email %d: status = %d, want 202", i, rr.Code)
			}
		}
		if rr := postInbound(t, s, "d.1@"+testInboundDomain, email(300)); rr.Code != http.StatusTooManyRequests {
			t.Errorf("email 301: status = %d, want 429", rr.Code)
		}
		if rr := postInbound(t, s, "other.1@"+testInboundDomain, email(0)); rr.Code != http.StatusAccepted {
			t.Errorf("another reader: status = %d, want 202", rr.Code)
		}
	})

	t.Run("pending bytes", func(t *testing.T) {
		store := knownInboundStore()
		store.storeErr = rss.ErrInboundFull
		s, jobs := newInboundServer(t, store)
		if rr := postInbound(t, s, "george.k7x2m9@"+testInboundDomain, email(0)); rr.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429", rr.Code)
		}
		if got := jobs.last(); got != "inbound rate_limited" {
			t.Errorf("outcome = %q, want inbound rate_limited", got)
		}
	})
}

func TestInboundEmailStoreFails(t *testing.T) {
	store := knownInboundStore()
	store.storeErr = errors.New("database down")
	s, _ := newInboundServer(t, store)
	if rr := postInbound(t, s, "george.k7x2m9@"+testInboundDomain, []byte(testEmail)); rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestParseInboundSignature(t *testing.T) {
	mac := strings.Repeat("ab", 32)
	tests := []struct {
		name, h string
		wantOK  bool
	}{
		{"valid", "t=1700000000,v1=" + mac, true},
		{"spaces", "t=1700000000, v1=" + mac, true},
		{"reversed", "v1=" + mac + ",t=1700000000", true},
		{"no t", "v1=" + mac, false},
		{"no v1", "t=1700000000", false},
		{"short MAC", "t=1700000000,v1=abab", false},
		{"not hex", "t=1700000000,v1=" + strings.Repeat("zz", 32), false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := parseInboundSignature(tt.h); ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

// inboundSignatureFixture is where TestInboundSignatureFixture writes signed
// requests, for infra/email-worker/worker.test.ts to check that the Worker
// signs the same: Go is the spec.
const inboundSignatureFixture = "../../infra/email-worker/testdata/inbound-signature.json"

// TestInboundSignatureFixture signs a few messages as inboundMAC does and
// checks infra/email-worker/testdata/inbound-signature.json holds the same
// X-Inbound-Signature values; with -update it writes the file instead.
func TestInboundSignatureFixture(t *testing.T) {
	type signed struct {
		Name      string `json:"name"`
		Secret    string `json:"secret"`
		T         string `json:"t"`
		To        string `json:"to"`
		Body      []byte `json:"body"` // base64 in JSON, so any bytes survive
		Signature string `json:"signature"`
	}
	cases := []signed{
		{Name: "a plain message", Secret: testInboundSecret, T: "1764201600", To: "sam.k7x2m9@" + testInboundDomain,
			Body: []byte("From: news@example.com\r\nTo: sam.k7x2m9@in.localhost\r\nSubject: Hello\r\n\r\nHi.\r\n")},
		{Name: "the recipient as sent, with case and a tag", Secret: testInboundSecret, T: "1764201601", To: "Sam.K7X2M9+news@In.Localhost",
			Body: []byte("Subject: Tagged\r\n\r\nHi.\r\n")},
		{Name: "UTF-8 and 8-bit bytes", Secret: "another secret, with ünïcode", T: "1764201602", To: "a.b@" + testInboundDomain,
			Body: append([]byte("Subject: =?UTF-8?Q?caf=C3=A9?=\r\n\r\ncafé "), 0x00, 0x80, 0xff, '\r', '\n')},
		{Name: "an empty body", Secret: testInboundSecret, T: "1764201603", To: "a.b@" + testInboundDomain, Body: []byte{}},
	}
	for i, c := range cases {
		cases[i].Signature = "t=" + c.T + ",v1=" + hex.EncodeToString(inboundMAC(c.Secret, c.T, c.To, c.Body))
	}

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cases); err != nil {
		t.Fatal(err)
	}
	if *updateFixtures {
		if err := os.WriteFile(inboundSignatureFixture, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(inboundSignatureFixture)
	if err != nil {
		t.Fatalf("%v; run: go test ./internal/server -run TestInboundSignatureFixture -update", err)
	}
	if !bytes.Equal(got, b.Bytes()) {
		t.Errorf("%s doesn't match inboundMAC; run: go test ./internal/server -run TestInboundSignatureFixture -update", inboundSignatureFixture)
	}
}
