package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Binding to localhost keeps the dashboard off the network but does nothing
// about DNS rebinding: a hostile page re-points its own domain at 127.0.0.1
// and the browser then treats fetch('/api/history') as same-origin. The only
// thing separating that request from a real one is the Host header.
func TestRequireLocalHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantCode int
	}{
		{"localhost with port", "localhost:9847", http.StatusOK},
		{"localhost bare", "localhost", http.StatusOK},
		{"loopback v4", "127.0.0.1:9847", http.StatusOK},
		{"loopback v6", "[::1]:9847", http.StatusOK},
		{"loopback v6 bare", "::1", http.StatusOK},

		{"rebound attacker domain", "evil.com:9847", http.StatusForbidden},
		{"attacker domain no port", "evil.com", http.StatusForbidden},
		{"localhost as a subdomain", "localhost.evil.com:9847", http.StatusForbidden},
		{"attacker domain ending in localhost", "evil-localhost:9847", http.StatusForbidden},
		{"lan address", "192.168.1.20:9847", http.StatusForbidden},
		{"empty host", "", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			h := requireLocalHost(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("Host %q: status = %d, want %d", tt.host, rec.Code, tt.wantCode)
			}
			if tt.wantCode != http.StatusOK && reached {
				t.Errorf("Host %q: request reached the handler; data would have been served", tt.host)
			}
		})
	}
}
