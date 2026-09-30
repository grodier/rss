package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	s := newTestServer(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthcheck", nil)
	s.router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want %q", got, "application/json")
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "deny" {
		t.Errorf("X-Frame-Options = %q; want %q", got, "deny")
	}

	var body struct {
		Status     string `json:"status"`
		SystemInfo struct {
			Environment string `json:"environment"`
		} `json:"system_info"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "available" {
		t.Errorf("status = %q; want %q", body.Status, "available")
	}
	if body.SystemInfo.Environment != "development" {
		t.Errorf("system_info.environment = %q; want %q", body.SystemInfo.Environment, "development")
	}
}
