//go:build linux && manual

package jump

import (
	"os"
	"strconv"
	"testing"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// Manual end-to-end checks against the real compositor. Excluded from normal
// runs by the "manual" tag because they need a graphical session and move the
// user's focus.
//
// A per-window terminal process is what makes matching exact. On a
// single-instance terminal (Ghostty as Omarchy starts it, kitty
// --single-instance) a session is instead identified by its own title, so pass
// the pid of a live session's agent process and the window it titled is the one
// that should be raised. To check the pre-title path, start a throwaway window:
//
//	ghostty --gtk-single-instance=false --title=livetest -e sleep 900
//
// then pass the pid of the `sleep` inside it -- no session owns it, so there is
// no title to match and ownership has to carry the pick alone.
func livePID(t *testing.T) int {
	t.Helper()
	pid, err := strconv.Atoi(os.Args[len(os.Args)-1])
	if err != nil {
		t.Fatalf("pass a pid as the last argument: %v", err)
	}
	return pid
}

// liveSession is the session csm itself would hand to Focus for this pid,
// discovered rather than fabricated: matching reads SessionTitle as well as the
// pid, so a hand-built Session exercises a shape Focus is never given on a
// single-instance terminal. A pid no session owns still works, with no title,
// which is exactly the fallback worth being able to aim at on purpose.
func liveSession(t *testing.T, confident bool) session.Session {
	t.Helper()
	pid := livePID(t)
	sessions, err := session.Discover()
	if err != nil {
		t.Logf("Discover() failed, going in with a bare pid: %v", err)
	}
	for _, s := range sessions {
		if s.GhostPID == pid {
			s.PIDConfident = confident
			t.Logf("pid %d is session %q titled %q", pid, s.Project, s.SessionTitle)
			return s
		}
	}
	t.Logf("no session owns pid %d; going in with no title", pid)
	return session.Session{GhostPID: pid, PIDConfident: confident}
}

// The exact path: one process per window, so the pid decides and the result is
// not a guess.
//
//	go test -tags manual ./internal/jump/ -run TestFocusLiveLinux -v -args <pid>
func TestFocusLiveLinux(t *testing.T) {
	s := liveSession(t, true)
	res, err := Focus(s)
	t.Logf("err=%v res=%+v msg=%q", err, res, res.Message())
	if err != nil {
		t.Fatalf("Focus() failed")
	}
	if res.Guessed {
		t.Error("Guessed is set for a confident session; the message will hedge for no reason")
	}
}

// The same window, reached from a pid that was paired to the session
// positionally -- the state a project sits in for half an hour after /clear or
// /resume. Focus must still work, and must say it is a guess.
//
//	go test -tags manual ./internal/jump/ -run TestFocusLiveLinuxAsGuess -v -args <pid>
func TestFocusLiveLinuxAsGuess(t *testing.T) {
	s := liveSession(t, false)
	res, err := Focus(s)
	t.Logf("err=%v res=%+v msg=%q", err, res, res.Message())
	if err != nil {
		t.Fatalf("Focus() failed for an unconfident pairing; it should jump and hedge, not decline")
	}
	if !res.Guessed {
		t.Error("Guessed is not set, so the message claims a certainty csm does not have")
	}
}

// What this Hyprland says when the window is gone, which is the one hyprctl
// answer that must not be mistaken for "you spelled the dispatch wrong": the
// two forms are tried in turn, and reporting the legacy form's syntax
// complaint instead of this would blame a version incompatibility that does
// not exist. Focuses nothing, because the address is dead.
//
//	go test -tags manual ./internal/jump/ -run TestHyprStaleWindowLinux -v
func TestHyprStaleWindowLinux(t *testing.T) {
	if _, err := detectBackend(); err != nil {
		t.Skipf("not on a supported compositor: %v", err)
	}
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		t.Skip("not Hyprland")
	}

	err := hyprland{}.focus("0xdeadbeef")
	t.Logf("focus(dead address) = %v; hyprWorkingForm = %d", err, hyprWorkingForm.Load())
	if err == nil {
		t.Fatal("focusing an address no window has succeeded, so 'ok' is not the only success signal after all")
	}
	if hyprRejectedForm(err.Error()) {
		t.Errorf("the failure reads as a rejected dispatch spelling (%v), so a gone window would send csm hunting for another form", err)
	}
}

// Reports what the compositor sees, which window each live session maps to,
// and the sentence the user would get -- without focusing anything.
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
		chosen, matches, ok := pickWindow(wins, chain, s.SessionTitle)
		outcome := ""
		if ok {
			outcome = (Result{Matches: matches, Noun: "window", Name: chosen.Title, Guessed: !s.PIDConfident}).Message()
		} else {
			outcome = noWindowError(b, matches, s).Error()
		}
		t.Logf("session %s pid=%d confident=%v origin=%q chain=%v -> ok=%v matches=%d %q\n    says: %s",
			s.Project, s.GhostPID, s.PIDConfident, s.Origin.App, chain, ok, matches, chosen.Title, outcome)
	}
}
