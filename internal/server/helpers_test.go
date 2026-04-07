package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	data := envelope{"message": "hello"}

	err := s.writeJSON(rr, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rr.Code != http.StatusOK {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "application/json")
	}

	var got envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["message"] != "hello" {
		t.Errorf("body message: got %q, want %q", got["message"], "hello")
	}
}

func TestWriteJSON_CustomHeaders(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	data := envelope{"ok": true}

	headers := http.Header{}
	headers.Set("X-Custom", "test-value")

	err := s.writeJSON(rr, http.StatusCreated, data, headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rr.Code != http.StatusCreated {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusCreated)
	}

	if got := rr.Header().Get("X-Custom"); got != "test-value" {
		t.Errorf("X-Custom header: got %q, want %q", got, "test-value")
	}

	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/json")
	}
}

func TestWriteJSON_UnmarshalableData(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	data := envelope{"bad": math.Inf(1)}

	err := s.writeJSON(rr, http.StatusOK, data, nil)
	if err == nil {
		t.Fatal("expected an error for unmarshalable data, got nil")
	}
}

func TestReadJSON(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	type input struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	body := `{"name": "Alice", "email": "alice@example.com"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst input
	err := s.readJSON(w, r, &dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst.Name != "Alice" {
		t.Errorf("Name: got %q, want %q", dst.Name, "Alice")
	}
	if dst.Email != "alice@example.com" {
		t.Errorf("Email: got %q, want %q", dst.Email, "alice@example.com")
	}
}

func TestReadJSON_EmptyBody(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	var dst struct{}
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}

	if !strings.Contains(err.Error(), "body must not be empty") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body must not be empty")
	}
}

func TestReadJSON_BadlyFormedJSON(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad json}"))
	w := httptest.NewRecorder()

	var dst struct{}
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for badly-formed JSON, got nil")
	}

	if !strings.Contains(err.Error(), "body contains badly-formed JSON") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body contains badly-formed JSON")
	}
}

func TestReadJSON_IncorrectType(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	type input struct {
		Age int `json:"age"`
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"age": "not a number"}`))
	w := httptest.NewRecorder()

	var dst input
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for incorrect type, got nil")
	}

	if !strings.Contains(err.Error(), "body contains incorrect JSON type") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body contains incorrect JSON type")
	}
}

func TestReadJSON_UnknownField(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	type input struct {
		Name string `json:"name"`
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name": "Alice", "unknown": "field"}`))
	w := httptest.NewRecorder()

	var dst input
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}

	if !strings.Contains(err.Error(), "body contains unknown key") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body contains unknown key")
	}
}

func TestReadJSON_MultipleJSONValues(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name": "Alice"}{"name": "Bob"}`))
	w := httptest.NewRecorder()

	var dst struct {
		Name string `json:"name"`
	}
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for multiple JSON values, got nil")
	}

	if !strings.Contains(err.Error(), "body must only contain a single JSON value") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body must only contain a single JSON value")
	}
}

func TestReadJSON_BodyTooLarge(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	// Create a body larger than 1MB
	large := `{"data": "` + strings.Repeat("x", 1_048_576+1) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(large))
	w := httptest.NewRecorder()

	var dst struct {
		Data string `json:"data"`
	}
	err := s.readJSON(w, r, &dst)
	if err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}

	if !strings.Contains(err.Error(), "body must not be larger than") {
		t.Errorf("error message: got %q, want it to contain %q", err.Error(), "body must not be larger than")
	}
}

