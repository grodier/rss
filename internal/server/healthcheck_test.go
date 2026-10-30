package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthcheck(t *testing.T) {
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", Version: "abc123"}, Services{Database: &fakeDatabase{}})

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
		Database   string `json:"database"`
		SystemInfo struct {
			Environment string `json:"environment"`
			Version     string `json:"version"`
		} `json:"system_info"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "available" {
		t.Errorf("status = %q; want %q", body.Status, "available")
	}
	if body.Database != "ok" {
		t.Errorf("database = %q; want %q", body.Database, "ok")
	}
	if body.SystemInfo.Environment != "development" {
		t.Errorf("system_info.environment = %q; want %q", body.SystemInfo.Environment, "development")
	}
	if body.SystemInfo.Version != "abc123" {
		t.Errorf("system_info.version = %q; want %q", body.SystemInfo.Version, "abc123")
	}
}

func TestHealthcheckDatabaseDown(t *testing.T) {
	db := &fakeDatabase{pingFn: func(context.Context) error { return errors.New("connection refused") }}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", Version: "abc123"}, Services{Database: db})

	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rr.Code, http.StatusOK)
	}
	raw := rr.Body.String()
	if strings.Contains(raw, "available") {
		t.Errorf("body contains the monitor's keyword %q: %s", "available", raw)
	}
	var body struct {
		Status     string `json:"status"`
		Database   string `json:"database"`
		SystemInfo struct {
			Version string `json:"version"`
		} `json:"system_info"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" || body.Database != "unreachable" {
		t.Errorf("status, database = %q, %q; want degraded, unreachable", body.Status, body.Database)
	}
	if body.SystemInfo.Version != "abc123" {
		t.Errorf("system_info.version = %q; want %q", body.SystemInfo.Version, "abc123")
	}
}

func TestHealthcheckDatabaseTimeout(t *testing.T) {
	var hadDeadline bool
	db := &fakeDatabase{pingFn: func(ctx context.Context) error {
		_, hadDeadline = ctx.Deadline()
		<-ctx.Done()
		return ctx.Err()
	}}
	s := newTestServerWith(t, Services{Database: db})

	rr := httptest.NewRecorder()
	start := time.Now()
	s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v; want under 2s", elapsed)
	}
	if !hadDeadline {
		t.Error("ping context has no deadline")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d; want %d", rr.Code, http.StatusOK)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q; want %q", body.Status, "degraded")
	}
}
