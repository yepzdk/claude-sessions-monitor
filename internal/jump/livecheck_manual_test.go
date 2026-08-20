//go:build darwin && manual

package jump

import (
	"os"
	"testing"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// Manual end-to-end check against a real Ghostty with live Claude sessions.
// Excluded from normal runs by the "manual" tag because it needs a GUI, live
// sessions, and Automation consent. Run it after a Ghostty update to confirm
// the AppleScript still works — especially once #11922 ships and the tty branch
// starts taking over:
//
//	go test -tags manual ./internal/jump/ -run TestFocusLive -v -args <projectPath>
func TestFocusLive(t *testing.T) {
	target := os.Args[len(os.Args)-1]
	sessions, _ := session.Discover()
	for _, s := range sessions {
		if s.CWD == target && s.GhostPID > 0 {
			res, err := Focus(s)
			t.Logf("err=%v res=%+v msg=%q", err, res, res.Message())
			return
		}
	}
	t.Fatalf("no live session for %s", target)
}
