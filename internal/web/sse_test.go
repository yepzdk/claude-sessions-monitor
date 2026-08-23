package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Run closes every client channel and returns when the context is cancelled.
// If register/unregister still block after that, each connected handler parks
// forever on its deferred unregister and never releases its goroutine.
func TestHandleSSEReturnsAfterHubShutdown(t *testing.T) {
	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

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
	go hub.Run(ctx)
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
