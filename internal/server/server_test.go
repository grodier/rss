package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
)

func newServerOnPort(t *testing.T, port int) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := NewServer(logger, Config{Port: port, Env: "development"}, Services{}, scs.New())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServeStopsWhenContextCanceled(t *testing.T) {
	s := newServerOnPort(t, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context was canceled")
	}
}

func TestServeReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := newServerOnPort(t, ln.Addr().(*net.TCPAddr).Port)

	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil, want a listen error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after listen failure")
	}
}
