package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

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
func (h *SSEHub) Run(ctx context.Context) {
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
			allSessions, err := session.Discover()
			if err != nil {
				// The browser stays connected and the heartbeat keeps firing,
				// so without saying anything the page would show stale state
				// under a "connected" indicator.
				h.broadcast(formatSSE("scan_error", []byte(`{"message":"session scan failed"}`)))
				continue
			}
			live := filterLiveSessions(allSessions)
			data, err := json.Marshal(live)
			if err != nil {
				continue
			}
			h.broadcast(formatSSE("sessions", data))

		case <-heartbeat.C:
			h.broadcast(formatSSE("heartbeat", []byte("{}")))
		}
	}
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

	// Send initial session data immediately (active + recently stopped sessions)
	allSessions, err := session.Discover()
	if err == nil {
		live := filterLiveSessions(allSessions)
		data, err := json.Marshal(live)
		if err == nil {
			w.Write(formatSSE("sessions", data))
			flusher.Flush()
		}
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
			w.Write(msg)
			flusher.Flush()
		}
	}
}
