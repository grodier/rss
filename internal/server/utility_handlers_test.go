package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type testServerOptions struct {
	version string
	env     string
}

func newTestServer(opts *testServerOptions) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	s := &Server{
		logger: logger,
	}

	if opts.version != "" {
		s.Version = opts.version
	}
	if opts.env != "" {
		s.Env = opts.env
	}

	return s
}

func TestHealthcheckHandler(t *testing.T) {
	version := "test-version"
	env := "test-env"
	s := newTestServer(&testServerOptions{
		version: version,
		env:     env,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/healtcheck", nil)
	rr := httptest.NewRecorder()

	s.healthcheckHandler(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("handler returned wrong content type: got %v want %v", contentType, "application/json")
	}

	var envelope map[string]any
	err := json.Unmarshal(rr.Body.Bytes(), &envelope)
	if err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	expectedStatus := "available"
	if envelope["status"] != expectedStatus {
		t.Errorf("expected status to be %v, got %v", expectedStatus, envelope["status"])
	}

	systemInfo, ok := envelope["system_info"].(map[string]any)
	if !ok {
		t.Fatalf("expected system_info to be a map")
	}

	if systemInfo["environment"] != env {
		t.Errorf("expected environment to be %v, got %v", env, systemInfo["environment"])
	}

	if systemInfo["version"] != version {
		t.Errorf("expected version to be %v, got %v", version, systemInfo["version"])
	}
}
