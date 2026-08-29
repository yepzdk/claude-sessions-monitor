//go:build linux

package jump

import "testing"

func TestPickWindowByProcessOwnership(t *testing.T) {
	// A terminal that forks per window: the window's pid is an ancestor of the
	// Claude process, and nothing else matches.
	wins := []window{
		{ID: "0xa", PID: 1000, Title: "~/Projects"},
		{ID: "0xb", PID: 2000, Title: "claude — fix the parser"},
		{ID: "0xc", PID: 3000, Title: "Spotify"},
	}
	chain := []int{5000, 4000, 2000, 1} // claude, shell, terminal, init

	got, matches, ok := pickWindow(wins, chain)
	if !ok {
		t.Fatal("pickWindow() found nothing, want the window owned by pid 2000")
	}
	if got.ID != "0xb" {
		t.Errorf("chose %q, want 0xb", got.ID)
	}
	if matches != 1 {
		t.Errorf("matches = %d, want 1 — an exact match is not a guess", matches)
	}
}

func TestPickWindowNoOwner(t *testing.T) {
	wins := []window{{ID: "0xa", PID: 1000, Title: "~/Projects"}}

	// A session running in a multiplexer or over SSH has no window here.
	if _, matches, ok := pickWindow(wins, []int{5000, 4000, 1}); ok || matches != 0 {
		t.Errorf("pickWindow() = (matches %d, ok %v), want (0, false)", matches, ok)
	}
}

func TestPickWindowSingleInstanceTerminal(t *testing.T) {
	// Ghostty with --gtk-single-instance: every window reports the same pid,
	// so ownership narrows to all of them and cannot go further.
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "◐ Reviewing the changelog"},
		{ID: "0xb", PID: 28701, Title: "π > Port project to Omarchy"},
		{ID: "0xc", PID: 28701, Title: "⏸ Rollercoaster | cliamp"},
	}
	chain := []int{5000, 4000, 28701, 1}

	// Three plausible windows and no way to separate them: declining beats
	// stealing focus to a window chosen at random.
	got, matches, ok := pickWindow(wins, chain)
	if ok {
		t.Errorf("pickWindow() chose %q from three indistinguishable windows, want no choice", got.ID)
	}
	if matches != 3 {
		t.Errorf("matches = %d, want 3 so the caller can say what happened", matches)
	}
}

func TestPickWindowGuessesPastPlainShells(t *testing.T) {
	// The common shape of the ambiguous case: one window running something,
	// the rest idle shells echoing their working directory.
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "…/Projects/personal/webwrap"},
		{ID: "0xb", PID: 28701, Title: "◐ Reviewing the changelog"},
		{ID: "0xc", PID: 28701, Title: "~/Work"},
	}
	chain := []int{5000, 4000, 28701, 1}

	got, matches, ok := pickWindow(wins, chain)
	if !ok {
		t.Fatal("pickWindow() found nothing, want the one window that is not an idle shell")
	}
	if got.ID != "0xb" {
		t.Errorf("chose %q, want 0xb", got.ID)
	}
	// The caller must hear that this was a guess among three.
	if matches != 3 {
		t.Errorf("matches = %d, want 3 so the result reads as a best guess", matches)
	}
}

func TestPickWindowIgnoresWindowsWithoutPIDs(t *testing.T) {
	// wmctrl reports 0 for windows whose owner it cannot determine. A zero pid
	// must never match a zero entry in the chain.
	wins := []window{
		{ID: "0xa", PID: 0, Title: "some window"},
		{ID: "0xb", PID: 2000, Title: "terminal"},
	}

	got, _, ok := pickWindow(wins, []int{0, 2000})
	if !ok || got.ID != "0xb" {
		t.Errorf("pickWindow() = (%q, ok %v), want 0xb", got.ID, ok)
	}
}
