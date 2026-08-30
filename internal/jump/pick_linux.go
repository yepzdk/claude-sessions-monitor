//go:build linux

package jump

import "strings"

// window is one top-level window as the compositor or window manager sees it.
type window struct {
	ID    string // whatever the backend needs to focus it again
	PID   int    // owning process, 0 when the backend does not report one
	Title string
}

// csmTitlePrefix is what csm titles its own window while the dashboard runs
// (ui.buildTerminalTitle, which prefixes every title it writes). On a terminal
// that runs one process for all of its windows, csm's window is a candidate by
// pid like any other -- and it is the one window that certainly does not hold
// the session, because it is where the user just pressed Enter.
const csmTitlePrefix = "CSM: "

// pickWindow chooses the window hosting a session, given the session process's
// ancestor chain (the process itself first, then its parents).
//
// Matching is by process ownership, not by title: a terminal that forks a
// process per window is an ancestor of the Claude process running inside it,
// so the window whose pid appears in the chain is that session's window. This
// is exact, unlike the directory heuristics the macOS path has to use.
//
// It stops being exact when one process owns several windows -- Ghostty with
// --gtk-single-instance, foot --server, kitty --single-instance. Every window
// then reports the same pid and the compositor knows nothing else about which
// is which. Where a title makes one candidate the obvious answer, that is used
// and reported as a guess; where it does not, the caller is told why rather
// than having a window picked for it at random.
//
// Returns the chosen window, how many candidates it was chosen from, and
// whether a choice was made at all. matches > 1 means the result is a guess.
func pickWindow(wins []window, chain []int) (chosen window, matches int, ok bool) {
	owned := windowsOwnedBy(withoutCSM(wins), chain)
	switch len(owned) {
	case 0:
		return window{}, 0, false
	case 1:
		return owned[0], 1, true
	}

	// Several windows, one process. A terminal echoing its working directory
	// ("…/Projects/personal/webwrap") is a plain shell; a window running
	// something announces what it is running. When exactly one candidate looks
	// like the latter, it is the answer -- but the caller still hears that it
	// was chosen from several, and Result.Message names the title it chose, so
	// a wrong guess is visible rather than silent.
	var named []window
	for _, w := range owned {
		if !looksLikePath(w.Title) {
			named = append(named, w)
		}
	}
	if len(named) == 1 {
		return named[0], len(owned), true
	}

	return window{}, len(owned), false
}

// withoutCSM drops csm's own window from the candidates. Filtering here rather
// than after ownership matching keeps it out of both the pid search and the
// count the user is shown: "2 windows matched" for a terminal holding the
// session and the dashboard watching it would be a count of nothing the user
// has a choice about.
func withoutCSM(wins []window) []window {
	kept := make([]window, 0, len(wins))
	for _, w := range wins {
		if strings.HasPrefix(w.Title, csmTitlePrefix) {
			continue
		}
		kept = append(kept, w)
	}
	return kept
}

// windowsOwnedBy returns the windows owned by the nearest ancestor in the
// chain that owns any.
//
// chain is nearest-first, and stopping at the first pid with windows is what
// makes the answer exact. Collecting from every ancestor instead would pull in
// the terminal *this* terminal was launched from, or an IDE's main process
// owning the editor window above a nested terminal -- several unrelated
// windows across several pids, which the caller can only read as one
// single-instance terminal it cannot disambiguate.
func windowsOwnedBy(wins []window, chain []int) []window {
	for _, pid := range chain {
		if pid <= 0 {
			continue
		}
		var owned []window
		for _, w := range wins {
			if w.PID == pid {
				owned = append(owned, w)
			}
		}
		if len(owned) > 0 {
			return owned
		}
	}
	return nil
}
