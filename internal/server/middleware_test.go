package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverPanicRendersHTML(t *testing.T) {
	s := newTestServer(t)
	h := s.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if got := rr.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection = %q, want close", got)
	}
}
