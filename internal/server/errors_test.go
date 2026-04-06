package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.errorResponse(rr, req, http.StatusBadRequest, "VALIDATION_ERROR", "something went wrong", map[string]string{"field": "email"})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "VALIDATION_ERROR" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "VALIDATION_ERROR")
	}

	if got["message"] != "something went wrong" {
		t.Errorf("message: got %q, want %q", got["message"], "something went wrong")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if details["field"] != "email" {
		t.Errorf("details.field: got %q, want %q", details["field"], "email")
	}
}

func TestErrorResponse_NilDetails(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.errorResponse(rr, req, http.StatusNotFound, "NOT_FOUND", "resource not found", nil)

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestNotFoundResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.notFoundResponse(rr, req)

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

	if got["message"] != "the requested resource could not be found" {
		t.Errorf("message: got %q, want %q", got["message"], "the requested resource could not be found")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestMethodNotAllowedResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	s.methodNotAllowedResponse(rr, req)

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

	if got["message"] != "the request method is not supported for this resource" {
		t.Errorf("message: got %q, want %q", got["message"], "the request method is not supported for this resource")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestServerErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	s := &Server{logger: logger}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	s.serverErrorResponse(rr, req, errors.New("db connection failed"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "INTERNAL_ERROR" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "INTERNAL_ERROR")
	}

	expectedMsg := "the server encountered a problem and could not process your request"
	if got["message"] != expectedMsg {
		t.Errorf("message: got %q, want %q", got["message"], expectedMsg)
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}

	logOutput := buf.String()
	if !bytes.Contains([]byte(logOutput), []byte("db connection failed")) {
		t.Errorf("expected log to contain error message, got: %s", logOutput)
	}
}
