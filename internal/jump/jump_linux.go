//go:build linux

package jump

import (
	"fmt"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// singleInstanceTerminals are the terminals that run one process for all of
// their windows *by choice*, so telling their users to turn that off is advice
// they can act on. Keyed by Origin.App, so only slugs in session.appCatalog
// can appear here -- `foot --server` behaves the same way but is not detected
// as an origin today.
//
// The other terminals that land in the ambiguous case -- GNOME Terminal via
// gnome-terminal-server, Terminator, Tilix, an IDE's integrated terminal --
// have no per-window process mode to disable, and offering them a flag that
// does not exist wastes the one line of feedback the dashboard has.
var singleInstanceTerminals = map[string]bool{
	"ghostty": true,
	"kitty":   true,
}

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

	if s.GhostPID <= 0 {
		return Result{}, fmt.Errorf("that session has no running process csm can find — jumping needs one to trace back to a window")
	}
	// GhostPID is exact when PIDConfident, and otherwise paired to the log
	// file by array position, which the discovery code documents as having no
	// real correspondence. That is not a reason to decline: a second .jsonl
	// appearing after /clear or /resume drops confidence for half an hour with
	// one unambiguous process running, and macOS degrades to directory
	// matching rather than refusing. So jump, and say it is a guess -- the
	// window title in the message is what makes a wrong pick visible.
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
	return Result{
		Matches: matches,
		Noun:    "window",
		Name:    chosen.Title,
		Guessed: !s.PIDConfident,
	}, nil
}

// noWindowError explains why nothing was focused. The two failures have
// opposite causes and opposite fixes, so they get different sentences.
func noWindowError(b backend, matches int, s session.Session) error {
	if matches == 0 {
		return fmt.Errorf("no %s window belongs to that session — it may be running in a different session, a multiplexer, or over SSH", b.name())
	}
	// One process, many windows: the compositor reports the same pid for all
	// of them and knows nothing else that separates them. Naming the terminal
	// makes the fix findable when there is one.
	what := s.Origin.Display
	if what == "" {
		what = "That terminal"
	}
	if singleInstanceTerminals[s.Origin.App] {
		return fmt.Errorf("%s runs as one process for all %d of its windows, so csm can't tell which one holds this session — starting it with its single-instance mode off makes jumping work",
			what, matches)
	}
	return fmt.Errorf("%s runs as one process for all %d of its windows, so csm can't tell which one holds this session",
		what, matches)
}
