package render

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	t0    = time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC)
	tNow  = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	tFrom = tNow.Add(-24 * time.Hour)
)

// fixtureServer answers like Render's API from testdata, counting requests
// and checking that each carries the key and the service.
func fixtureServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("%s: Authorization = %q", r.URL.Path, got)
		}
		var file string
		switch {
		case strings.HasPrefix(r.URL.Path, "/metrics/"):
			q := r.URL.Query()
			if q.Get("resource") != "srv-test" || q.Get("startTime") != "2026-11-08T12:00:00Z" ||
				q.Get("endTime") != "2026-11-09T12:00:00Z" || q.Get("resolutionSeconds") != "600" {
				t.Errorf("%s: query %s", r.URL.Path, r.URL.RawQuery)
			}
			file = strings.TrimPrefix(r.URL.Path, "/metrics/")
		case r.URL.Path == "/services/srv-test/deploys":
			if got := r.URL.Query().Get("limit"); got != "100" {
				t.Errorf("deploys: limit = %q, want 100", got)
			}
			file = "deploys"
		default:
			http.NotFound(w, r)
			return
		}
		body, err := os.ReadFile("testdata/" + file + ".json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// testClient is a client of srv whose clock is *now.
func testClient(srv *httptest.Server, now *time.Time) *Client {
	c := New("test-key", "srv-test")
	c.baseURL = srv.URL
	c.now = func() time.Time { return *now }
	return c
}

func TestInstances(t *testing.T) {
	srv, _ := fixtureServer(t)
	now := tNow
	got, err := testClient(srv, &now).Instances(context.Background(), tFrom, tNow, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Two instances overlap at 11:10: the highest counts.
	wantCPU := []Point{{t0, 0.012}, {t0.Add(10 * time.Minute), 0.051}, {t0.Add(20 * time.Minute), 0.02}}
	if !equalPoints(got.CPU, wantCPU) {
		t.Errorf("CPU = %v, want %v", got.CPU, wantCPU)
	}
	if want := []Point{{t0, 100 << 20}, {t0.Add(10 * time.Minute), 120 << 20}}; !equalPoints(got.Memory, want) {
		t.Errorf("Memory = %v, want %v", got.Memory, want)
	}
	if want := []Point{{t0, 1}, {t0.Add(10 * time.Minute), 2}, {t0.Add(20 * time.Minute), 1}}; !equalPoints(got.Count, want) {
		t.Errorf("Count = %v, want %v", got.Count, want)
	}
	if got.CPULimit != 0.5 || got.MemoryLimit != 512<<20 {
		t.Errorf("limits = %v CPU, %v bytes, want 0.5 and 512 MiB", got.CPULimit, got.MemoryLimit)
	}
}

func TestDeploys(t *testing.T) {
	srv, _ := fixtureServer(t)
	now := tNow
	got, err := testClient(srv, &now).Deploys(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []Deploy{
		{ID: "dep-running", Status: "update_in_progress", Trigger: "api", CreatedAt: tNow.Add(-time.Minute)},
		{ID: "dep-new", Status: "live", Trigger: "api", Image: "sha256:" + strings.Repeat("b", 64), CreatedAt: tNow.Add(-2 * time.Hour), FinishedAt: tNow.Add(-2*time.Hour + 2*time.Minute)},
		// Without a resolved digest, the one in the reference.
		{ID: "dep-failed", Status: "update_failed", Trigger: "api", Image: "sha256:" + strings.Repeat("c", 64), CreatedAt: tNow.Add(-3 * time.Hour), FinishedAt: tNow.Add(-3*time.Hour + time.Minute)},
		{ID: "dep-old", Status: "deactivated", Trigger: "manual", Image: "sha256:" + strings.Repeat("a", 64), CreatedAt: tNow.Add(-24 * time.Hour), FinishedAt: tNow.Add(-24*time.Hour + 3*time.Minute)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d deploys, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("deploy %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, d := range got {
		if live := d.ID == "dep-new" || d.ID == "dep-old"; d.WentLive() != live {
			t.Errorf("%s: WentLive = %v, want %v", d.ID, d.WentLive(), live)
		}
	}
}

func TestCache(t *testing.T) {
	srv, requests := fixtureServer(t)
	now := tNow
	c := testClient(srv, &now)
	ctx := context.Background()
	for range 2 {
		if _, err := c.Deploys(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests within a minute, want 1", n)
	}
	now = now.Add(CacheFor)
	if _, err := c.Deploys(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 2 {
		t.Errorf("%d requests after a minute, want 2", n)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		ctx     func() (context.Context, context.CancelFunc)
		want    string
	}{
		{
			name: "unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			},
			want: "status 401",
		},
		{
			name:    "malformed JSON",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[{"deploy":`)) },
			want:    "unexpected end of JSON input",
		},
		{
			name:    "too large",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat(" ", maxBody+1))) },
			want:    "answer over",
		},
		{
			name: "timeout",
			handler: func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
			want: "context deadline exceeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				tt.handler(w, r)
			}))
			defer srv.Close()
			now := tNow
			c := testClient(srv, &now)
			ctx, cancel := context.Background(), context.CancelFunc(func() {})
			if tt.ctx != nil {
				ctx, cancel = tt.ctx()
			}
			defer cancel()

			_, err := c.Deploys(ctx, 10)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Deploys error = %v, want one containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Errorf("error %q has the API key", err)
			}
			_, err = c.Instances(ctx, tFrom, tNow, 10*time.Minute)
			if err == nil {
				t.Fatal("Instances error = nil")
			}
			// Failures aren't cached.
			before := requests.Load()
			c.Deploys(ctx, 10)
			if requests.Load() == before && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Error("a failed answer was cached")
			}
		})
	}
}

func equalPoints(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].T.Equal(b[i].T) || a[i].V != b[i].V {
			return false
		}
	}
	return true
}
