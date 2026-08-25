package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// Run closes every client channel and returns when the context is cancelled.
// If register/unregister still block after that, each connected handler parks
// forever on its deferred unregister and never releases its goroutine.
func TestHandleSSEReturnsAfterHubShutdown(t *testing.T) {
	// HandleSSE scans for sessions before it streams. Pointed at a real home
	// directory that scan takes seconds, and the test would measure the
	// developer's session count instead of the shutdown path.
	t.Setenv("HOME", t.TempDir())

	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx, make(chan error, 1))

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleSSE))
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Get(srv.URL)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 256)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()

	// Let the handler register before shutting the hub down.
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE client did not finish after hub shutdown; " +
			"the handler is blocked sending to unregister with no receiver")
	}
}

// A client that connects while the hub is already shutting down must not park
// in register waiting for a receiver that has gone away.
func TestHandleSSEDoesNotBlockWhenHubAlreadyStopped(t *testing.T) {
	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx, make(chan error, 1))
	cancel()
	time.Sleep(100 * time.Millisecond) // let Run return

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.HandleSSE(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HandleSSE blocked on register after the hub stopped")
	}
}

// csm runs this hub in the same process as a terminal held in raw mode on the
// alternate screen. A panic that kills the goroutine where it happens skips
// the caller's deferred restore, dropping the user at an echoless prompt with
// no way to tell csm crashed. The panic must reach the caller instead.
func TestRunReportsScannerPanicInsteadOfCrashing(t *testing.T) {
	orig := discoverSessions
	discoverSessions = func() ([]session.Session, error) {
		panic("malformed log")
	}
	defer func() { discoverSessions = orig }()

	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fatal := make(chan error, 1)
	go hub.Run(ctx, fatal)

	// The scan is skipped while nobody is connected, so the panic needs a
	// registered client before the tick can reach it.
	client := make(chan []byte, 1)
	hub.register <- client

	select {
	case err := <-fatal:
		if !strings.Contains(err.Error(), "malformed log") {
			t.Errorf("report does not name the panic: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic never reached the caller; csm would have crashed with the terminal still in raw mode")
	}

	select {
	case <-hub.done:
	case <-time.After(time.Second):
		t.Error("done was not closed, so connected handlers park forever")
	}
}
