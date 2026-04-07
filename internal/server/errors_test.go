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

func TestBadRequestResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	s.badRequestResponse(rr, req, errors.New("invalid JSON body"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "BAD_REQUEST" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "BAD_REQUEST")
	}

	if got["message"] != "invalid JSON body" {
		t.Errorf("message: got %q, want %q", got["message"], "invalid JSON body")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestUnauthorizedResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.unauthorizedResponse(rr, req, "invalid or expired token")

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "UNAUTHENTICATED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "UNAUTHENTICATED")
	}

	if got["message"] != "invalid or expired token" {
		t.Errorf("message: got %q, want %q", got["message"], "invalid or expired token")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestForbiddenResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.forbiddenResponse(rr, req, "STEP_UP_REQUIRED", "re-authentication required for this action")

	if rr.Code != http.StatusForbidden {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "STEP_UP_REQUIRED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "STEP_UP_REQUIRED")
	}

	if got["message"] != "re-authentication required for this action" {
		t.Errorf("message: got %q, want %q", got["message"], "re-authentication required for this action")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestForbiddenResponse_ForbiddenCode(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	s.forbiddenResponse(rr, req, "FORBIDDEN", "you do not have permission to access this resource")

	if rr.Code != http.StatusForbidden {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "FORBIDDEN" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "FORBIDDEN")
	}
}

func TestRateLimitedResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	s.rateLimitedResponse(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "RATE_LIMITED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "RATE_LIMITED")
	}

	if got["message"] != "you have exceeded the rate limit, please try again later" {
		t.Errorf("message: got %q, want %q", got["message"], "you have exceeded the rate limit, please try again later")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestConflictResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	s.conflictResponse(rr, req, "EMAIL_UNAVAILABLE", "an account with this email already exists")

	if rr.Code != http.StatusConflict {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusConflict)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "EMAIL_UNAVAILABLE" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "EMAIL_UNAVAILABLE")
	}

	if got["message"] != "an account with this email already exists" {
		t.Errorf("message: got %q, want %q", got["message"], "an account with this email already exists")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestValidationErrorResponse(t *testing.T) {
	s := newTestServer(&testServerOptions{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	fieldErrors := map[string]string{
		"email":    "must be a valid email address",
		"password": "must be at least 8 characters",
	}

	s.validationErrorResponse(rr, req, fieldErrors)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "INVALID_INPUT" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "INVALID_INPUT")
	}

	if got["message"] != "the request failed validation" {
		t.Errorf("message: got %q, want %q", got["message"], "the request failed validation")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if details["email"] != "must be a valid email address" {
		t.Errorf("details.email: got %q, want %q", details["email"], "must be a valid email address")
	}

	if details["password"] != "must be at least 8 characters" {
		t.Errorf("details.password: got %q, want %q", details["password"], "must be at least 8 characters")
	}
}
