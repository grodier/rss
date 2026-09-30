package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticRoutes(t *testing.T) {
	s := newTestServer(t)
	h := s.router()

	tests := []struct {
		path        string
		wantStatus  int
		wantContent string
	}{
		{"/static/", http.StatusNotFound, ""},
		{"/static/css/", http.StatusNotFound, ""},
		{"/static/css/main.css", http.StatusOK, "text/css"},
		{"/static/js/main.js", http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tt.wantContent) {
				t.Errorf("Content-Type = %q, want it to contain %q", ct, tt.wantContent)
			}
		})
	}
}
