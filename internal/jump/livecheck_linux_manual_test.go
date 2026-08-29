//go:build linux && manual

package jump

import (
	"os"
	"strconv"
	"testing"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// Manual end-to-end check against the real compositor. Excluded from normal
// runs by the "manual" tag because it needs a graphical session and moves the
// user's focus.
//
// Pass any pid running inside the terminal window you expect to be raised --
// a live Claude session's, or a `sleep` you started there:
//
//	go test -tags manual ./internal/jump/ -run TestFocusLiveLinux -v -args <pid>
func TestFocusLiveLinux(t *testing.T) {
	pid, err := strconv.Atoi(os.Args[len(os.Args)-1])
	if err != nil {
		t.Fatalf("pass a pid as the last argument: %v", err)
	}

	s := session.Session{GhostPID: pid, PIDConfident: true}
	res, err := Focus(s)
	t.Logf("err=%v res=%+v msg=%q", err, res, res.Message())
	if err != nil {
		t.Fatalf("Focus() failed")
	}
}

// Reports what the compositor sees and which window each live session maps to,
// without focusing anything.
//
//	go test -tags manual ./internal/jump/ -run TestInspectLinux -v
func TestInspectLinux(t *testing.T) {
	b, err := detectBackend()
	if err != nil {
		t.Fatalf("detectBackend() = %v", err)
	}
	wins, err := b.list()
	if err != nil {
		t.Fatalf("list() = %v", err)
	}
	t.Logf("backend %s reports %d windows", b.name(), len(wins))
	for _, w := range wins {
		t.Logf("  pid=%-8d %-14s %q", w.PID, w.ID, w.Title)
	}

	sessions, _ := session.Discover()
	for _, s := range sessions {
		if s.GhostPID <= 0 {
			continue
		}
		chain := session.AncestorPIDs(s.GhostPID)
		chosen, matches, ok := pickWindow(wins, chain)
		t.Logf("session %s pid=%d confident=%v chain=%v -> ok=%v matches=%d %q",
			s.Project, s.GhostPID, s.PIDConfident, chain, ok, matches, chosen.Title)
	}
}
