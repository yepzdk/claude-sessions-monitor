//go:build linux

package jump

// window is one top-level window as the compositor or window manager sees it.
type window struct {
	ID    string // whatever the backend needs to focus it again
	PID   int    // owning process, 0 when the backend does not report one
	Title string
	Class string
}

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
	owned := windowsOwnedBy(wins, chain)
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
	// was chosen from several.
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

// windowsOwnedBy returns the windows whose pid appears in the process chain.
func windowsOwnedBy(wins []window, chain []int) []window {
	inChain := make(map[int]struct{}, len(chain))
	for _, pid := range chain {
		if pid > 0 {
			inChain[pid] = struct{}{}
		}
	}

	var owned []window
	for _, w := range wins {
		if w.PID <= 0 {
			continue
		}
		if _, hit := inChain[w.PID]; hit {
			owned = append(owned, w)
		}
	}
	return owned
}
