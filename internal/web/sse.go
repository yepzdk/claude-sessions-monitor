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
}

// NewSSEHub creates a new SSE hub
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    make(map[chan []byte]struct{}),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
	}
}

// Run starts the SSE hub, broadcasting session updates every 2s
func (h *SSEHub) Run(ctx context.Context) {
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
			allSessions, err := session.Discover()
			if err != nil {
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
	h.register <- client

	// Send initial data immediately (active + recently stopped sessions)
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
		h.unregister <- client
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
