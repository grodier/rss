package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
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

