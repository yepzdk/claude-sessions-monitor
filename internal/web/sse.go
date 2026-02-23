package web

import (
	"context"
	"encoding/json"
	"fmt"
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
			active := make([]session.Session, 0, len(allSessions))
			for _, s := range allSessions {
				if s.Status != session.StatusInactive {
					active = append(active, s)
				}
			}
			data, err := json.Marshal(active)
			if err != nil {
				continue
			}
			msg := fmt.Appendf(nil, "event: sessions\ndata: %s\n\n", data)
			h.broadcast(msg)

		case <-heartbeat.C:
			msg := []byte("event: heartbeat\ndata: {}\n\n")
			h.broadcast(msg)
		}
	}
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

	// Send initial data immediately (active sessions only)
	allSessions, err := session.Discover()
	if err == nil {
		active := make([]session.Session, 0, len(allSessions))
		for _, s := range allSessions {
			if s.Status != session.StatusInactive {
				active = append(active, s)
			}
		}
		data, err := json.Marshal(active)
		if err == nil {
			fmt.Fprintf(w, "event: sessions\ndata: %s\n\n", data)
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
