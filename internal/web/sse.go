package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// discoverSessions is a seam: the scan is the only thing in this package that
// can panic, and it cannot be driven from a test through the real filesystem.
var discoverSessions = session.Discover

// SSEHub manages Server-Sent Events connections
type SSEHub struct {
	clients    map[chan []byte]struct{}
	register   chan chan []byte
	unregister chan chan []byte
	mu         sync.Mutex
	// done is closed when Run returns. register and unregister are unbuffered,
	// so without a way to observe that Run has stopped receiving, every handler
	// still in flight parks on its deferred unregister forever.
	done chan struct{}
}

// NewSSEHub creates a new SSE hub
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    make(map[chan []byte]struct{}),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		done:       make(chan struct{}),
	}
}

// Run starts the SSE hub, broadcasting session updates every 2s for as
// long as a dashboard is connected.
//
// It owns the hub's client set until it returns, at which point it closes done
// so that handlers blocked on register or unregister can give up.
//
// A panic in the scan is still fatal, but it travels to fatal instead of
// killing the goroutine where it happened: csm shares this process with a
// terminal held in raw mode on the alternate screen, and only the caller's
// deferred restore can hand it back. Crashing here would drop the user at an
// echoless prompt with the trace painted over a screen that is about to be
// discarded.
func (h *SSEHub) Run(ctx context.Context, fatal chan<- error) {
	defer func() {
		if r := recover(); r != nil {
			fatal <- fmt.Errorf("session scanner panicked: %v\n\n%s", r, debug.Stack())
		}
	}()
	defer close(h.done)

	ticker := time.NewTicker(2 * time.Second)
	heartbeat := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for ch := range h.clients {
				close(ch)
				delete(h.clients, ch)
			}
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				close(client)
				delete(h.clients, client)
			}
			h.mu.Unlock()

		case <-ticker.C:
			// Discover walks ~/.claude/projects and parses JSONL. With no
			// dashboard open there is nobody to send the result to, and doing
			// it anyway burns disk and CPU for as long as csm is running.
			if !h.hasClients() {
				continue
			}
			allSessions, err := discoverSessions()
			if err != nil {
				// The browser stays connected and the heartbeat keeps firing,
				// so without saying anything the page would show stale state
				// under a "connected" indicator.
				h.broadcast(formatSSE("scan_error", []byte(`{"message":"session scan failed"}`)))
				continue
			}
			for _, frame := range sessionFrames(allSessions) {
				h.broadcast(frame)
			}

		case <-heartbeat.C:
			h.broadcast(formatSSE("heartbeat", []byte("{}")))
		}
	}
}

// sessionFrames builds the frames a session update is made of, ready to send in
// the order returned: the badge decision, then the rows it applies to. A sender
// writes them in order and does not decide that order for itself, which is what
// keeps the two senders from drifting.
//
// A frame that cannot be marshalled is absent rather than empty, so a sender
// that writes everything returned always sends a well-formed stream. Both
// frames follow that one rule, so neither sender has a failure case to handle.
func sessionFrames(all []session.Session) [][]byte {
	var frames [][]byte
	if harness := harnessEvent(all); harness != nil {
		frames = append(frames, harness)
	}
	data, err := json.Marshal(filterLiveSessions(all))
	if err != nil {
		return frames
	}
	return append(frames, formatSSE("sessions", data))
}

// harnessEvent renders the badge decision for these sessions as an SSE frame,
// or nil when it cannot be marshalled.
//
// Whether a card names its agent is decided from every session on the machine,
// and what the dashboard is sent is filterLiveSessions' last hour of them: the
// browser cannot see the other agent's idle sessions, so deriving this in the
// page would drop the badge exactly when the machine is running both agents and
// one of them happens to be quiet.
//
// Its own event rather than a field folded into the sessions payload, whose
// array shape /api/sessions publishes. Every sender emits it immediately before
// the rows it applies to, so no frame is ever rendered without it.
func harnessEvent(all []session.Session) []byte {
	data, err := json.Marshal(map[string]bool{"mixed": session.MixedHarnesses(all)})
	if err != nil {
		return nil
	}
	return formatSSE("harnesses", data)
}

// formatSSE formats an SSE message safely. If data contains literal newlines
// (which json.Marshal should not produce, but as defense-in-depth), each line
// gets its own "data:" prefix per the SSE specification.
func formatSSE(event string, data []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(event)
	buf.WriteByte('\n')
	for _, line := range bytes.Split(data, []byte("\n")) {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// hasClients reports whether any dashboard is currently connected.
func (h *SSEHub) hasClients() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients) > 0
}

func (h *SSEHub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// Client too slow, drop it
			close(ch)
			delete(h.clients, ch)
		}
	}
}

// HandleSSE handles SSE client connections
func (h *SSEHub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	client := make(chan []byte, 16)
	select {
	case h.register <- client:
	case <-h.done:
		// The hub stopped before we joined; nothing will ever be sent.
		return
	case <-r.Context().Done():
		return
	}

	// Send initial session data immediately (active + recently stopped sessions),
	// preceded by the badge decision: this payload is what the page renders
	// first, and without the flag that frame would draw every card untagged
	// until the next broadcast two seconds later.
	allSessions, err := discoverSessions()
	if err == nil {
		// A failed write means the client is already gone. Returning here would
		// leave it registered with the hub, because the unregister defer is not
		// set until below; the r.Context().Done() case takes it off moments
		// later.
		for _, frame := range sessionFrames(allSessions) {
			_, _ = w.Write(frame)
		}
		flusher.Flush()
	}

	defer func() {
		select {
		case h.unregister <- client:
		case <-h.done:
			// Run already closed every client channel on its way out.
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-client:
			if !ok {
				return
			}
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}
