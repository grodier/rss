package httpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/form/v4"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestServe(t *testing.T) {
	t.Run("returns when ctx is canceled", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		srv := &http.Server{Addr: "127.0.0.1:0"}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, srv, logger, "test server", "port", 0) }()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve returned %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after ctx was canceled")
		}
		log := buf.String()
		for _, want := range []string{`msg="starting test server" port=0`, `msg="shutting down test server"`, `msg="test server stopped gracefully"`} {
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %s:\n%s", want, log)
			}
		}
	})

	t.Run("returns the listen error for a port in use", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		srv := &http.Server{Addr: ln.Addr().String()}

		done := make(chan error, 1)
		go func() { done <- Serve(context.Background(), srv, discardLogger(), "test server") }()

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("Serve returned nil, want a listen error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after the listen failure")
		}
	})
}

func TestParseTemplates(t *testing.T) {
	fsys := fstest.MapFS{
		"pages/home.html":         {Data: []byte(`{{template "greeting" .}} {{shout .}}`)},
		"pages/about.html":        {Data: []byte(`about`)},
		"pages/partials/one.html": {Data: []byte(`{{define "greeting"}}hello{{end}}`)},
	}
	funcs := template.FuncMap{"shout": strings.ToUpper}

	ts, err := ParseTemplates(fsys, "pages/*.html", "pages/partials/*.html", funcs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts["home.html"] == nil || ts["about.html"] == nil {
		t.Fatalf("templates = %v, want home.html and about.html", ts)
	}
	var b strings.Builder
	if err := ts["home.html"].Execute(&b, "x"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "hello X" {
		t.Errorf("home.html = %q, want %q", b.String(), "hello X")
	}

	fsys["pages/broken.html"] = &fstest.MapFile{Data: []byte(`{{nope}}`)}
	if _, err := ParseTemplates(fsys, "pages/*.html", "pages/partials/*.html", funcs); err == nil || !strings.Contains(err.Error(), "pages/broken.html") {
		t.Errorf("error = %v, want one naming pages/broken.html", err)
	}
}

func TestRenderHTML(t *testing.T) {
	t.Run("writes the status and content type", func(t *testing.T) {
		tmpl := template.Must(template.New("page").Parse(`<p>{{.}}</p>`))
		rr := httptest.NewRecorder()
		if err := RenderHTML(rr, http.StatusTeapot, tmpl, "hi"); err != nil {
			t.Fatal(err)
		}
		if rr.Code != http.StatusTeapot {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusTeapot)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}
		if rr.Body.String() != "<p>hi</p>" {
			t.Errorf("body = %q", rr.Body)
		}
	})

	t.Run("a failing template writes nothing", func(t *testing.T) {
		tmpl := template.Must(template.New("page").Parse(`<p>half</p>{{.Missing}}`))
		rr := httptest.NewRecorder()
		if err := RenderHTML(rr, http.StatusOK, tmpl, 42); err == nil {
			t.Fatal("RenderHTML returned nil, want the template's error")
		}
		if rr.Body.Len() != 0 || len(rr.Header()) != 0 {
			t.Errorf("wrote a response: headers %v, body %q", rr.Header(), rr.Body)
		}
	})
}

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	if err := WriteJSON(rr, http.StatusCreated, map[string]any{"b": 1, "a": "x"}); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusCreated)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if want := `{"a":"x","b":1}` + "\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body, want)
	}
}

func TestDecodePostForm(t *testing.T) {
	type loginForm struct {
		Email string `form:"email"`
	}
	post := func(body string) (*httptest.ResponseRecorder, *http.Request) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return httptest.NewRecorder(), req
	}
	dec := form.NewDecoder()

	t.Run("decodes", func(t *testing.T) {
		w, r := post(url.Values{"email": {"a@example.com"}}.Encode())
		var f loginForm
		if err := DecodePostForm(w, r, dec, &f, MaxFormBytes); err != nil {
			t.Fatal(err)
		}
		if f.Email != "a@example.com" {
			t.Errorf("Email = %q", f.Email)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		w, r := post("email=%zz")
		var f loginForm
		err := DecodePostForm(w, r, dec, &f, MaxFormBytes)
		if err == nil {
			t.Fatal("no error for a malformed body")
		}
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			t.Errorf("error = %v, a MaxBytesError for a small body", err)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		for _, limit := range []int64{MaxFormBytes, 1 << 10} {
			w, r := post("email=" + strings.Repeat("a", int(limit)))
			var f loginForm
			err := DecodePostForm(w, r, dec, &f, limit)
			if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
				t.Errorf("limit %d: error = %v, want *http.MaxBytesError", limit, err)
			}
		}
	})

	t.Run("a non-pointer dst panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("no panic for a non-pointer dst")
			}
		}()
		w, r := post("email=a")
		DecodePostForm(w, r, dec, loginForm{}, MaxFormBytes)
	})
}

func TestClientIP(t *testing.T) {
	const warning = "client IP from X-Forwarded-For is not a public address"
	newClientIP := func() (*ClientIP, *bytes.Buffer) {
		var buf bytes.Buffer
		return &ClientIP{ProxyHops: 1, Logger: slog.New(slog.NewTextHandler(&buf, nil))}, &buf
	}
	of := func(c *ClientIP, xff string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:5000"
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return c.Of(req)
	}

	t.Run("warns once across many requests", func(t *testing.T) {
		c, buf := newClientIP()
		if got := of(c, "10.0.0.5"); got != "10.0.0.5" {
			t.Errorf("Of() = %q, want %q", got, "10.0.0.5")
		}
		for _, xff := range []string{"192.168.1.1", "10.0.0.6", "172.16.0.1"} {
			of(c, xff)
		}
		if n := strings.Count(buf.String(), warning); n != 1 {
			t.Fatalf("%d warnings, want 1; log:\n%s", n, buf)
		}
		if !strings.Contains(buf.String(), "ip=10.0.0.5") || !strings.Contains(buf.String(), "proxy_hops=1") {
			t.Errorf("warning doesn't name the address and hops; log:\n%s", buf)
		}
	})

	t.Run("a nil Logger logs to the default logger", func(t *testing.T) {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })
		of(&ClientIP{ProxyHops: 1}, "10.0.0.5")
		if !strings.Contains(buf.String(), warning) {
			t.Errorf("no warning on the default logger; log:\n%s", buf.String())
		}
	})

	t.Run("a public address isn't logged", func(t *testing.T) {
		c, buf := newClientIP()
		if got := of(c, "198.51.100.7"); got != "198.51.100.7" {
			t.Errorf("Of() = %q, want %q", got, "198.51.100.7")
		}
		if buf.Len() != 0 {
			t.Errorf("public address logged:\n%s", buf)
		}
	})

	t.Run("without the header, the peer isn't logged", func(t *testing.T) {
		c, buf := newClientIP()
		if got := of(c, ""); got != "10.1.2.3" {
			t.Errorf("Of() without header = %q, want %q", got, "10.1.2.3")
		}
		if buf.Len() != 0 {
			t.Errorf("fallback logged:\n%s", buf)
		}
	})
}

func TestLogRequests(t *testing.T) {
	tests := []struct {
		name       string
		h          http.HandlerFunc
		wantStatus float64
		wantSize   float64
	}{
		{"explicit status and body", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("hello"))
		}, 201, 5},
		{"nothing written", func(w http.ResponseWriter, r *http.Request) {}, 200, 0},
		{"body without WriteHeader", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hi"))
		}, 200, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			clientIP := func(r *http.Request) string { return "192.0.2.7" }
			req := httptest.NewRequest(http.MethodGet, "/some/path?q=1", nil)
			LogRequests(logger, clientIP)(tt.h).ServeHTTP(httptest.NewRecorder(), req)

			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("%v: %s", err, buf.String())
			}
			if got["msg"] != "request" || got["status"] != tt.wantStatus || got["size"] != tt.wantSize {
				t.Errorf("line = %v, want a request line with status %v and size %v", got, tt.wantStatus, tt.wantSize)
			}
			if got["method"] != "GET" || got["uri"] != "/some/path?q=1" || got["ip"] != "192.0.2.7" || got["proto"] != "HTTP/1.1" {
				t.Errorf("unexpected fields: %v", got)
			}
			if _, ok := got["duration"]; !ok {
				t.Error("missing duration")
			}
		})
	}
}

func TestSafeReturnPath(t *testing.T) {
	const fallback = "/feeds/abc"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"local path", "/sites/abc", "/sites/abc"},
		{"query string", "/discover?q=x", "/discover?q=x"},
		{"fragment", "/sites/abc#feed-x", "/sites/abc#feed-x"},
		{"empty", "", fallback},
		{"relative path", "feeds/x", fallback},
		{"protocol-relative", "//evil.example", fallback},
		{"backslash host", `/\evil.example`, fallback},
		{"absolute URL", "https://evil.example/", fallback},
		{"javascript scheme", "javascript:alert(1)", fallback},
		{"control character", "/sites/abc\n", fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeReturnPath(tt.in, fallback); got != tt.want {
				t.Errorf("SafeReturnPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAllowedMethods(t *testing.T) {
	var got []string
	r := chi.NewRouter()
	r.Use(middleware.GetHead)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		got = AllowedMethods(r)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	noop := func(w http.ResponseWriter, r *http.Request) {}
	r.Get("/both", noop)
	r.Post("/both", noop)
	r.Post("/post", noop)
	r.Get("/items/{id}", noop)

	tests := []struct {
		path string
		want string
	}{
		{"/both", "GET, HEAD, POST"},
		{"/post", "POST"},
		{"/items/42", "GET, HEAD"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got = nil
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, tt.path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rr.Code)
			}
			if s := strings.Join(got, ", "); s != tt.want {
				t.Errorf("AllowedMethods = %q, want %q", s, tt.want)
			}
		})
	}

	t.Run("no route context", func(t *testing.T) {
		if got := AllowedMethods(httptest.NewRequest(http.MethodDelete, "/both", nil)); got != nil {
			t.Errorf("AllowedMethods = %v, want nil", got)
		}
	})
}

// pinger is a database whose ping calls fn.
type pinger struct {
	fn func(context.Context) error
}

func (p pinger) PingContext(ctx context.Context) error { return p.fn(ctx) }

func TestHealth(t *testing.T) {
	t.Run("database answers", func(t *testing.T) {
		body, err := Health(context.Background(), pinger{func(context.Context) error { return nil }}, "development", "abc123")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"status":      "available",
			"database":    "ok",
			"system_info": map[string]any{"environment": "development", "version": "abc123"},
		}
		if a, b := mustJSON(t, body), mustJSON(t, want); a != b {
			t.Errorf("body = %s, want %s", a, b)
		}
	})

	t.Run("failing ping", func(t *testing.T) {
		pingErr := errors.New("connection refused")
		body, err := Health(context.Background(), pinger{func(context.Context) error { return pingErr }}, "production", "abc123")
		if !errors.Is(err, pingErr) {
			t.Errorf("err = %v, want the ping's", err)
		}
		if body["status"] != "degraded" || body["database"] != "unreachable" {
			t.Errorf("status, database = %v, %v; want degraded, unreachable", body["status"], body["database"])
		}
		// The uptime monitor's keyword is "available": a degraded body
		// mustn't contain it anywhere.
		if raw := mustJSON(t, body); strings.Contains(raw, "available") {
			t.Errorf("body contains the monitor's keyword %q: %s", "available", raw)
		}
		if !strings.Contains(mustJSON(t, body), `"version":"abc123"`) {
			t.Errorf("body lacks the version: %s", mustJSON(t, body))
		}
	})

	t.Run("the ping has a deadline", func(t *testing.T) {
		// The fake reports the timeout at once rather than waiting for it.
		var deadline time.Time
		body, err := Health(context.Background(), pinger{func(ctx context.Context) error {
			deadline, _ = ctx.Deadline()
			return context.DeadlineExceeded
		}}, "development", "abc123")
		if deadline.IsZero() {
			t.Fatal("ping context has no deadline")
		}
		if latest := time.Now().Add(healthTimeout); deadline.After(latest) {
			t.Errorf("ping deadline %v is more than %v away", deadline, healthTimeout)
		}
		if err == nil || body["status"] != "degraded" {
			t.Errorf("err, status = %v, %v; want an error and degraded", err, body["status"])
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
