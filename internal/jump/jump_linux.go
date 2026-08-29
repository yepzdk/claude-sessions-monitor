//go:build linux

package jump

import (
	"fmt"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// Focus brings the window hosting this session to the front.
//
// Unlike the macOS path, which asks Ghostty to focus a specific tab, this
// works at the window level: Linux terminals have no scripting interface csm
// can rely on, so a session in a background tab gets its window raised and no
// more. Window-level is also why matching can be exact -- see pickWindow.
func Focus(s session.Session) (Result, error) {
	b, err := detectBackend()
	if err != nil {
		return Result{}, err
	}

	// Only match when we actually know which process is this session's.
	// GhostPID is otherwise paired to the log file by array position, which
	// the discovery code documents as having no real correspondence, and
	// focusing a sibling session's window while reporting full confidence is
	// worse than declining.
	if !s.PIDConfident || s.GhostPID <= 0 {
		return Result{}, fmt.Errorf("don't know which process is this session — jumping needs a running session csm can identify")
	}
	chain := session.AncestorPIDs(s.GhostPID)
	if len(chain) == 0 {
		return Result{}, fmt.Errorf("that session's process is gone")
	}

	wins, err := b.list()
	if err != nil {
		return Result{}, fmt.Errorf("couldn't ask %s for its windows: %w", b.name(), err)
	}

	chosen, matches, ok := pickWindow(wins, chain)
	if !ok {
		return Result{}, noWindowError(b, matches, s)
	}

	if err := b.focus(chosen.ID); err != nil {
		return Result{}, fmt.Errorf("couldn't focus the window: %w", err)
	}
	return Result{Matches: matches, Name: chosen.Title}, nil
}

// noWindowError explains why nothing was focused. The two failures have
// opposite causes and opposite fixes, so they get different sentences.
func noWindowError(b backend, matches int, s session.Session) error {
	if matches == 0 {
		return fmt.Errorf("no %s window belongs to that session — it may be running in a different session, a multiplexer, or over SSH", b.name())
	}
	// One process, many windows: the compositor reports the same pid for all
	// of them and knows nothing else that separates them. Naming the terminal
	// makes the fix findable, because it is a launch flag on that terminal.
	what := s.Origin.Display
	if what == "" {
		what = "that terminal"
	}
	return fmt.Errorf("%s runs as one process for all %d of its windows, so csm can't tell which one holds this session — start it with its single-instance mode off to make jumping work",
		what, matches)
}
