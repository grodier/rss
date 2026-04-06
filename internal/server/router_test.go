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

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "METHOD_NOT_ALLOWED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "METHOD_NOT_ALLOWED")
	}

	if got["message"] == nil || got["message"] == "" {
		t.Error("expected non-empty message")
	}

	if _, ok := got["details"].(map[string]any); !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
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

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "NOT_FOUND" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "NOT_FOUND")
	}

	if got["message"] == nil || got["message"] == "" {
		t.Error("expected non-empty message")
	}

	if _, ok := got["details"].(map[string]any); !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}
}
