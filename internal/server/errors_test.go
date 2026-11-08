package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

func TestServerErrorRecorded(t *testing.T) {
	tests := []struct {
		name    string
		respond func(s *Server, w http.ResponseWriter, r *http.Request, err error)
	}{
		{"HTML", (*Server).serverErrorHTML},
		{"JSON", (*Server).serverErrorJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &fakeErrorLog{}
			s := newTestServerWith(t, Services{ErrorLog: log})
			router := chi.NewRouter()
			router.Post("/feeds/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
				tt.respond(s, w, r, errors.New("query feeds: connection refused"))
			})

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/feeds/abc123/refresh?token=secret", nil))

			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rr.Code)
			}
			got := log.recorded()
			if len(got) != 1 {
				t.Fatalf("recorded %d errors, want 1", len(got))
			}
			want := rss.ServerError{Route: "/feeds/{id}/refresh", Method: "POST", Message: "query feeds: connection refused"}
			if got[0] != want {
				t.Errorf("recorded %+v, want %+v", got[0], want)
			}
		})
	}

	t.Run("outside every route", func(t *testing.T) {
		log := &fakeErrorLog{}
		s := newTestServerWith(t, Services{ErrorLog: log})
		rr := httptest.NewRecorder()
		s.serverErrorHTML(rr, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("boom"))
		if got := log.recorded(); len(got) != 1 || got[0].Route != metrics.Unmatched {
			t.Errorf("recorded %+v, want one on %s", got, metrics.Unmatched)
		}
	})
}

func TestPanicRecorded(t *testing.T) {
	log := &fakeErrorLog{}
	s := newTestServerWith(t, Services{ErrorLog: log})
	// The real router's middleware order, around a route that panics.
	router := chi.NewRouter()
	router.Use(s.recordRequest, s.serverTiming, s.recoverPanic)
	router.Get("/boom/{id}", func(w http.ResponseWriter, r *http.Request) { panic("boom") })

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom/1", nil))

	got := log.recorded()
	if len(got) != 1 {
		t.Fatalf("recorded %d errors, want 1", len(got))
	}
	want := rss.ServerError{Route: "/boom/{id}", Method: "GET", Message: "boom", Panic: true}
	if got[0] != want {
		t.Errorf("recorded %+v, want %+v", got[0], want)
	}
}

func TestErrorLogFull(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	// Nothing drains this log, as when the database is down: fill it.
	errorLog := metrics.NewErrorLog(nil, "web-1", logger)
	for range metrics.ErrorLogCapacity {
		errorLog.Record(rss.ServerError{Route: "/", Method: "GET", Message: "earlier"})
	}
	s := newTestServerWith(t, Services{ErrorLog: errorLog})
	s.logger = logger

	done := make(chan *httptest.ResponseRecorder)
	go func() {
		rr := httptest.NewRecorder()
		s.serverErrorHTML(rr, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("boom"))
		done <- rr
	}()
	select {
	case rr := <-done:
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the response waited for the full error log")
	}
	if !strings.Contains(logs.String(), "error log full") {
		t.Errorf("logs = %q, want a warning that the error was dropped", logs.String())
	}
}
