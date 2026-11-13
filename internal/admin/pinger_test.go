package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeAvailabilityStore records what the pinger stores.
type fakeAvailabilityStore struct {
	checks        []rss.AvailabilityCheck
	deletedBefore time.Time
	err           error
}

func (f *fakeAvailabilityStore) Record(ctx context.Context, c rss.AvailabilityCheck) error {
	f.checks = append(f.checks, c)
	return f.err
}

func (f *fakeAvailabilityStore) DeleteBefore(ctx context.Context, before time.Time) error {
	f.deletedBefore = before
	return f.err
}

// newPinger returns a pinger checking a test server that runs handler.
func newPinger(t *testing.T, handler http.HandlerFunc) (*Pinger, *fakeAvailabilityStore) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	store := &fakeAvailabilityStore{}
	return &Pinger{URL: ts.URL, Store: store, Timeout: 100 * time.Millisecond}, store
}

// answer returns a handler answering /healthcheck with status and body.
func answer(t *testing.T, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthcheck" || r.Method != http.MethodGet {
			t.Errorf("request %s %s, want GET /healthcheck", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

func TestPingerCheck(t *testing.T) {
	tests := []struct {
		name        string
		handler     func(t *testing.T) http.HandlerFunc
		wantOK      bool
		wantStatus  string
		wantVersion string
		wantLatency bool
	}{
		{
			name: "available",
			handler: func(t *testing.T) http.HandlerFunc {
				return answer(t, 200, `{"status":"available","database":"ok","system_info":{"environment":"production","version":"abc123"}}`)
			},
			wantOK:      true,
			wantStatus:  "available",
			wantVersion: "abc123",
			wantLatency: true,
		},
		{
			name: "degraded",
			handler: func(t *testing.T) http.HandlerFunc {
				return answer(t, 200, `{"status":"degraded","database":"unreachable","system_info":{"version":"abc123"}}`)
			},
			wantStatus:  "degraded",
			wantVersion: "abc123",
			wantLatency: true,
		},
		{
			name: "timeout",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
					}
				}
			},
			wantStatus: rss.CheckTimeout,
		},
		{
			name:       "server error",
			handler:    func(t *testing.T) http.HandlerFunc { return answer(t, 500, `{"status":"available"}`) },
			wantStatus: rss.CheckError,
		},
		{
			name:       "not JSON",
			handler:    func(t *testing.T) http.HandlerFunc { return answer(t, 200, `<html>`) },
			wantStatus: rss.CheckError,
		},
		{
			name:       "no status",
			handler:    func(t *testing.T) http.HandlerFunc { return answer(t, 200, `{}`) },
			wantStatus: rss.CheckError,
		},
		{
			name: "redirect",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/elsewhere", http.StatusFound)
				}
			},
			wantStatus: rss.CheckError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newPinger(t, tt.handler(t))
			before := time.Now()
			c, err := p.Check(context.Background())
			if c.OK != tt.wantOK || c.Status != tt.wantStatus || c.Version != tt.wantVersion {
				t.Errorf("ok, status, version = %v, %q, %q; want %v, %q, %q", c.OK, c.Status, c.Version, tt.wantOK, tt.wantStatus, tt.wantVersion)
			}
			if (err == nil) != tt.wantOK {
				t.Errorf("err = %v, want an error unless ok", err)
			}
			if (c.LatencyMs != nil) != tt.wantLatency {
				t.Errorf("latency = %v, want set: %v", c.LatencyMs, tt.wantLatency)
			} else if c.LatencyMs != nil && *c.LatencyMs <= 0 {
				t.Errorf("latency = %v ms, want above 0", *c.LatencyMs)
			}
			if c.CheckedAt.Before(before) || c.CheckedAt.After(time.Now()) {
				t.Errorf("checked at %v, want during the check", c.CheckedAt)
			}
		})
	}
}

// TestPingerReadsWWWsHealthcheck checks the pinger against www's real
// /healthcheck answers, written by internal/server's TestHealthcheckFixture,
// so a renamed key in either fails here rather than in production.
func TestPingerReadsWWWsHealthcheck(t *testing.T) {
	raw, err := os.ReadFile("../server/testdata/healthcheck.json")
	if err != nil {
		t.Fatal(err)
	}
	var answers map[string]string
	if err := json.Unmarshal(raw, &answers); err != nil {
		t.Fatal(err)
	}
	for status, wantOK := range map[string]bool{"available": true, "degraded": false} {
		t.Run(status, func(t *testing.T) {
			p, _ := newPinger(t, answer(t, 200, answers[status]))
			c, _ := p.Check(context.Background())
			if c.OK != wantOK || c.Status != status || c.Version != "abc123" {
				t.Errorf("check = %+v; want ok %v, status %s, version abc123", c, wantOK, status)
			}
		})
	}
}

func TestPingerCheckLongStatusError(t *testing.T) {
	p, _ := newPinger(t, answer(t, 200, `{"status":"`+strings.Repeat("x", 5000)+`"}`))
	_, err := p.Check(context.Background())
	if err == nil || len(err.Error()) > 200 {
		t.Errorf("err = %d bytes, want the status cut to 100 characters", len(err.Error()))
	}
}

func TestPingerCheckUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()
	p := &Pinger{URL: url, Store: &fakeAvailabilityStore{}}
	c, err := p.Check(context.Background())
	if err == nil || c.OK || c.Status != rss.CheckError || c.LatencyMs != nil {
		t.Errorf("check = %+v, %v; want an error with no latency", c, err)
	}
}

func TestPingerPing(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	p, store := newPinger(t, func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.Write([]byte(`{"status":"available"}`))
		} else {
			w.Write([]byte(`{"status":"degraded"}`))
		}
	})
	ctx := context.Background()

	p.Ping(ctx)
	healthy.Store(false)
	p.Ping(ctx)
	var got []string
	for _, c := range store.checks {
		got = append(got, c.Status)
	}
	if want := "available degraded"; strings.Join(got, " ") != want {
		t.Errorf("recorded %v, want %s", got, want)
	}

	// A failed write is logged, not fatal.
	store.err = errors.New("database down")
	p.Ping(ctx)
	if len(store.checks) != 3 {
		t.Errorf("recorded %d checks, want 3", len(store.checks))
	}
}

func TestPingerPingCanceled(t *testing.T) {
	p, store := newPinger(t, answer(t, 200, `{"status":"available"}`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Ping(ctx)
	if len(store.checks) != 0 {
		t.Errorf("recorded %+v while shutting down, want nothing", store.checks)
	}
}

func TestPingerTidy(t *testing.T) {
	store := &fakeAvailabilityStore{}
	p := &Pinger{Store: store}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	p.Tidy(context.Background(), now)
	if want := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC); !store.deletedBefore.Equal(want) {
		t.Errorf("deleted checks before %v, want %v (90 days)", store.deletedBefore, want)
	}
}
