package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
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
		Handler: securityHeaders(mux),
	}

	// Start SSE hub
	go s.hub.Run(ctx)

	// Bind listener synchronously so caller knows if port is available
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("port %d is already in use. Use --port <number> to specify a different port, or check what's using it: lsof -i :%d", s.port, s.port)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.Serve(ln); err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Shut down when context is cancelled
	go func() {
		<-ctx.Done()
		s.server.Close()
	}()

	return errCh, nil
}

// securityHeaders wraps an http.Handler to set standard security headers
// on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'")
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
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
