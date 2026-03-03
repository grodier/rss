package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes_HealthcheckGET(t *testing.T) {
	s := newTestServer(&testServerOptions{version: "1.0.0", env: "testing"})
	router := s.router()

	req := httptest.NewRequest(http.MethodGet, "/v1/healthcheck", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusOK)
	}

	var got envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["status"] != "available" {
		t.Errorf("status: got %q, want %q", got["status"], "available")
	}
}

func TestRoutes_HealthcheckMethodNotAllowed(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	router := s.router()

	req := httptest.NewRequest(http.MethodPost, "/v1/healthcheck", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestRoutes_NotFound(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	router := s.router()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}
