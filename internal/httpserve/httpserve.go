// Package httpserve is the HTTP plumbing www's and the admin's servers
// share: serving and shutting down, rendering pages and JSON, reading forms,
// the client's address and the request log, the open-redirect check, the 405's
// Allow header and the healthcheck's body. What differs on purpose (headers,
// error pages, panics, authentication) stays in each server.
package httpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/form/v4"
	"github.com/grodier/rss/internal/clientip"
)

// shutdownTimeout is how long Serve waits for requests in flight once ctx
// is canceled.
const shutdownTimeout = 30 * time.Second

// Serve runs srv until ctx is canceled, then shuts it down, waiting up to
// 30 seconds for requests in flight. name labels its log lines ("server",
// "admin server"); attrs are logged with the start line. It returns early
// with the error if srv fails to start, e.g. the port is in use.
func Serve(ctx context.Context, srv *http.Server, logger *slog.Logger, name string, attrs ...any) error {
	logger.Info("starting "+name, attrs...)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		// Only Serve calls Shutdown, so this is a startup/listen failure.
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down " + name)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// ListenAndServe returns ErrServerClosed as soon as Shutdown starts.
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	logger.Info(name + " stopped gracefully")
	return nil
}

// ParseTemplates parses each page matching pages in fsys with the
// templates matching partials and funcs, keyed by the page's base name.
func ParseTemplates(fsys fs.FS, pages, partials string, funcs template.FuncMap) (map[string]*template.Template, error) {
	files, err := fs.Glob(fsys, pages)
	if err != nil {
		return nil, err
	}

	ts := map[string]*template.Template{}
	for _, page := range files {
		name := path.Base(page)
		t, err := template.New(name).Funcs(funcs).ParseFS(fsys, page, partials)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		ts[name] = t
	}
	return ts, nil
}

// RenderHTML executes t with data into a buffer, then writes it with status,
// so a template error never sends half a page.
func RenderHTML(w http.ResponseWriter, status int, t *template.Template, data any) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// WriteJSON writes data as JSON, with a trailing newline, with status.
func WriteJSON(w http.ResponseWriter, status int, data any) error {
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}
	js = append(js, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(js)
	return err
}

// MaxFormBytes caps a posted form's body unless a route allows more.
const MaxFormBytes = 64 << 10

// DecodePostForm parses r's posted form, at most limit bytes, into dst with
// dec. It panics if dst isn't a pointer to a struct (a programming error).
// Over the limit, the error is an *http.MaxBytesError.
func DecodePostForm(w http.ResponseWriter, r *http.Request, dec *form.Decoder, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		return err
	}
	if err := dec.Decode(dst, r.PostForm); err != nil {
		if _, ok := errors.AsType[*form.InvalidDecoderError](err); ok {
			panic(err)
		}
		return err
	}
	return nil
}

// ClientIP finds a request's client address behind ProxyHops proxies
// (clientip.FromRequest), logging once if X-Forwarded-For gives an address
// that isn't public: that is what an extra proxy hop inside the host's
// network looks like. A nil Logger logs to slog.Default().
type ClientIP struct {
	ProxyHops int
	Logger    *slog.Logger
	warned    sync.Once
}

// Of returns the address of the client that sent r.
func (c *ClientIP) Of(r *http.Request) string {
	ip, notPublic := clientip.FromRequest(r, c.ProxyHops)
	if notPublic {
		c.warned.Do(func() {
			entries := strings.Count(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",") + 1
			logger := c.Logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("client IP from X-Forwarded-For is not a public address; check -proxy-hops", "ip", ip, "entries", entries, "proxy_hops", c.ProxyHops)
		})
	}
	return ip
}

// LogRequests logs every request with its client address, status, size and
// duration.
func LogRequests(logger *slog.Logger, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // nothing was written: net/http sends 200
			}
			logger.Info("request",
				"ip", clientIP(r), "proto", r.Proto, "method", r.Method, "uri", r.RequestURI,
				"status", status, "size", ww.BytesWritten(), "duration", time.Since(start))
		})
	}
}

// SafeReturnPath returns p if it's a local path, which can't send the
// browser to another site, else fallback. Prevents open redirects.
func SafeReturnPath(p, fallback string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return fallback
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return p
}

// allowOrder is the methods AllowedMethods probes, in the order an Allow
// header lists them.
var allowOrder = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// AllowedMethods returns the methods r's router serves r's path with, in
// the order an Allow header lists them, HEAD wherever GET is
// (middleware.GetHead). It's nil when r has no chi route context. chi
// doesn't set Allow when a custom 405 handler is installed, so the handler
// asks this.
func AllowedMethods(r *http.Request) []string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil || rctx.Routes == nil {
		return nil
	}
	var allowed []string
	for _, m := range allowOrder {
		probe := m
		if m == http.MethodHead {
			probe = http.MethodGet // GetHead serves HEAD with the GET handler
		}
		// A fresh context: Match would modify the request's own.
		if rctx.Routes.Match(chi.NewRouteContext(), probe, r.URL.Path) {
			allowed = append(allowed, m)
		}
	}
	return allowed
}

// healthTimeout bounds the healthcheck's database ping, so a hung database
// makes the check say "degraded" rather than hang.
const healthTimeout = time.Second

// Health pings the database within a second and returns the healthcheck's
// body: "available" or "degraded", with env and version. err is the ping's
// error, for the caller to log. The uptime monitor reads the body's
// "status", so "degraded" must not contain its keyword "available".
func Health(ctx context.Context, db interface{ PingContext(context.Context) error }, env, version string) (body map[string]any, err error) {
	status, database := "available", "ok"
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		status, database = "degraded", "unreachable"
	}
	return map[string]any{
		"status":   status,
		"database": database,
		"system_info": map[string]any{
			"environment": env,
			"version":     version,
		},
	}, err
}
