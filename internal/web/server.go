// Package web serves the browser dashboard: a loopback-only HTTP server with a
// small JSON API, a Server-Sent Events stream that pushes session updates, and
// the frontend (vanilla JS, no build step) embedded from ./static via go:embed.
// The server never reads logs itself; everything comes from package session.
//
// See docs/ARCHITECTURE.md for the route table, the SSE cadence, and the
// security decisions (Host check, headers, the deliberately unset WriteTimeout).
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"time"
)

//go:embed static
var staticFiles embed.FS

// Server is the web dashboard HTTP server
type Server struct {
	port   int
	hub    *SSEHub
	server *http.Server
}

// NewServer creates a new web dashboard server
func NewServer(port int) *Server {
	return &Server{
		port: port,
		hub:  NewSSEHub(),
	}
}

// Start starts the web server in the background. It returns once the server
// is listening, or returns an error if it fails to bind. The server runs
// until ctx is cancelled. Any serve error is sent on the returned channel.
func (s *Server) Start(ctx context.Context) (<-chan error, error) {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/sessions", handleSessions)
	mux.HandleFunc("/api/history", handleHistory)
	mux.HandleFunc("/api/sessions/timeline", handleTimeline)
	mux.HandleFunc("/api/sessions/metrics", handleMetrics)
	mux.HandleFunc("/api/usage", handleUsage)
	mux.HandleFunc("/api/quota", handleQuota)
	mux.HandleFunc("/api/claude-status", handleClaudeStatus)
	mux.HandleFunc("/api/events", s.hub.HandleSSE)

	// Static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("failed to create sub filesystem: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	addr := fmt.Sprintf("localhost:%d", s.port)
	s.server = &http.Server{
		Addr:    addr,
		Handler: requireLocalHost(securityHeaders(mux)),
		// A request that dribbles in one header byte at a time otherwise holds
		// a goroutine and an fd for as long as it likes.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
		// WriteTimeout is deliberately unset: it applies to the whole response,
		// so any value would cut every SSE stream off at the deadline.
	}

	// Bind listener synchronously so caller knows if port is available
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hint := fmt.Sprintf("lsof -i :%d", s.port)
		if runtime.GOOS == "linux" {
			hint = fmt.Sprintf("ss -tlnp | grep :%d", s.port)
		}
		return nil, fmt.Errorf("failed to listen on port %d: %w\nUse --port <number> to specify a different port, or check what's using it: %s", s.port, err, hint)
	}

	// Buffered for both senders below, each of which reports at most once, so
	// neither can block on a caller that has stopped listening.
	errCh := make(chan error, 2)
	go s.hub.Run(ctx, errCh)
	go func() {
		if err := s.server.Serve(ln); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Shut down when context is cancelled
	go func() {
		<-ctx.Done()
		// Close only reports errors from connections of a server that is
		// already being torn down.
		_ = s.server.Close()
	}()

	return errCh, nil
}

// requireLocalHost rejects requests whose Host header is not a loopback name.
//
// Listening on localhost keeps the dashboard off the network, but it does not
// stop DNS rebinding: an attacker points their own domain at 127.0.0.1, and the
// browser then considers a fetch to this server same-origin and sends it. The
// Host header still carries the attacker's domain, and it is the only part of
// such a request that gives it away. Without this check a page the user merely
// visits can read /api/history and every session timeline behind it -- prompt
// text, file paths, and anything pasted into a session.
func requireLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		// Host carries a port for every request the dashboard actually serves,
		// but it is optional, and SplitHostPort fails rather than passing the
		// bare name through.
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch host {
		case "localhost", "127.0.0.1", "::1":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "forbidden host", http.StatusForbidden)
		}
	})
}

// securityHeaders wraps an http.Handler to set standard security headers
// on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'")
		// embed.FS reports a zero mod-time for every file, which browsers
		// treat as a green light for long heuristic caching -- without this,
		// a csm upgrade can leave the dashboard silently stuck on old
		// HTML/CSS/JS until the user thinks to hard-refresh.
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// Addr returns the address the server is configured to listen on.
func (s *Server) Addr() string {
	return fmt.Sprintf("localhost:%d", s.port)
}

// ProbeCSMServer checks if a csm web server is already running on the given port
// by making a quick HTTP GET to the sessions API endpoint.
func ProbeCSMServer(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/sessions", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
