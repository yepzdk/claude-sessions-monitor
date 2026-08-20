package jump

import "strings"

// candidate is one terminal surface reported by the terminal app.
type candidate struct {
	ID   string
	TTY  string // "/dev/ttys002", empty when the terminal app doesn't expose it
	Dir  string // current working directory
	Name string // current tab title
}

// pick chooses the terminal hosting the session identified by tty and dir.
//
// Matching by tty is exact: a tty belongs to exactly one terminal surface, so a
// hit there ends the search. Ghostty only exposes tty from the release that
// includes ghostty-org/ghostty#11922; before that every candidate has an empty
// TTY and we fall back to the working directory.
//
// ponytail: directory matching is a heuristic; it goes away once Ghostty ships
// #11922 and every candidate carries a tty. No version check needed — the tty
// branch above simply starts hitting.
//
// Directory matching is ambiguous by nature — a project often has a Claude tab
// and a plain shell open side by side. Claude titles its tab after the current
// task ("✳ Fix the pagination bug"), while an idle shell titles itself after
// its own path ("…/Projects/personal/webwrap"), so a name that doesn't look
// like a path is the better guess.
//
// Returns the chosen candidate and how many candidates matched, so the caller
// can tell the user when the choice was a guess. matches == 0 means no match.
func pick(cands []candidate, tty, dir string) (chosen candidate, matches int) {
	if tty != "" {
		for _, c := range cands {
			if c.TTY != "" && c.TTY == tty {
				return c, 1
			}
		}
	}

	if dir == "" {
		return candidate{}, 0
	}

	var byDir []candidate
	for _, c := range cands {
		if c.Dir == dir {
			byDir = append(byDir, c)
		}
	}
	if len(byDir) == 0 {
		return candidate{}, 0
	}

	for _, c := range byDir {
		if !looksLikePath(c.Name) {
			return c, len(byDir)
		}
	}
	return byDir[0], len(byDir)
}

// looksLikePath reports whether a tab title is a terminal echoing its working
// directory rather than a Claude session announcing its task. Shells render a
// shortened path ("…/Projects/personal/webwrap"), sometimes with a leading "~".
func looksLikePath(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true // nothing to go on; treat as the weaker candidate
	}
	return strings.HasPrefix(name, "…") ||
		strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "~")
}
