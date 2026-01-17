package watcher

import (
	"context"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// Watcher polls the filesystem for session changes
type Watcher struct {
	interval time.Duration
}

// New creates a new watcher with the specified polling interval
func New(interval time.Duration) *Watcher {
	return &Watcher{
		interval: interval,
	}
}

// Watch starts polling and sends session updates to the callback
// It runs until the context is cancelled
func (w *Watcher) Watch(ctx context.Context, callback func([]session.Session)) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Initial scan
	sessions, _ := session.Discover()
	callback(sessions)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			sessions, err := session.Discover()
			if err != nil {
				continue
			}
			callback(sessions)
		}
	}
}
