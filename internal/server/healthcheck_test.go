package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// healthcheckFixture holds /healthcheck's answers, available and degraded,
// with their status codes, which the admin's pinger reads
// (internal/admin/pinger_test.go): it decodes them with its own type, so a
// renamed key or a new status code fails its test, not production.
const healthcheckFixture = "testdata/healthcheck.json"

func TestHealthcheckFixture(t *testing.T) {
	type answer struct {
		Code int    `json:"code"`
		Body string `json:"body"`
	}
	get := func(db *fakeDatabase) answer {
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", Version: "abc123"}, Services{Database: db})
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))
		return answer{rr.Code, rr.Body.String()}
	}
	down := &fakeDatabase{pingFn: func(context.Context) error { return errors.New("connection refused") }}
	fixture := map[string]answer{"available": get(&fakeDatabase{}), "degraded": get(down)}

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fixture); err != nil {
		t.Fatal(err)
	}
	if *updateFixtures {
		if err := os.WriteFile(healthcheckFixture, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(healthcheckFixture)
	if err != nil {
		t.Fatalf("%v; run: go test ./internal/server -run TestHealthcheckFixture -update", err)
	}
	if !bytes.Equal(got, b.Bytes()) {
		t.Errorf("%s doesn't match /healthcheck's answers; run: go test ./internal/server -run TestHealthcheckFixture -update", healthcheckFixture)
	}
}

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
