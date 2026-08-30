package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
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
		defer func() { _ = resp.Body.Close() }()
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

// The dashboard cannot work out whether to name each card's agent: the rows it
// is sent are only the last hour, and the sessions that make the machine a
// two-agent machine are usually older than that. So the server decides, and
// says so before the rows arrive -- a client that renders on the sessions event
// must already hold the answer.
func TestRunSendsTheHarnessDecisionBeforeTheRowsItAppliesTo(t *testing.T) {
	orig := discoverSessions
	t.Cleanup(func() { discoverSessions = orig })
	discoverSessions = func() ([]session.Session, error) {
		return []session.Session{
			{Project: "api", Harness: session.HarnessOMP, Status: session.StatusWorking, LastActivity: time.Now()},
			// Idle for hours: filterLiveSessions drops it, so the browser never
			// sees the second agent this machine is running.
			{Project: "web", Harness: session.HarnessClaude, Status: session.StatusInactive,
				LastActivity: time.Now().Add(-4 * time.Hour)},
		}, nil
	}

	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx, make(chan error, 1))

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleSSE))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// One broadcast tick is 2s; read until both events have arrived.
	stream := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if strings.Contains(b.String(), "event: sessions") || err != nil {
				stream <- b.String()
				return
			}
		}
	}()

	var got string
	select {
	case got = <-stream:
	case <-time.After(8 * time.Second):
		t.Fatal("no sessions event arrived")
	}

	harnesses := strings.Index(got, "event: harnesses")
	sessions := strings.Index(got, "event: sessions")
	if harnesses < 0 {
		t.Fatalf("no harnesses event in the stream; the dashboard has nothing to decide the badge from:\n%s", got)
	}
	if harnesses > sessions {
		t.Error("the rows arrived before the harness decision, so the first frame renders them untagged")
	}
	if !strings.Contains(got[harnesses:sessions], `"mixed":true`) {
		t.Errorf("harness decision was not mixed, though the machine has an idle Claude session:\n%s", got[harnesses:sessions])
	}

	// The decision is machine-wide; the rows themselves are still the live ones.
	if strings.Contains(got[sessions:], `"project":"web"`) {
		t.Error("a session idle for hours was sent as a live row")
	}
}
